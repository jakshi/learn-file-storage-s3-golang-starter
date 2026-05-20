package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"io"
	"mime"
	"net/http"
	"os"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/google/uuid"
)

func generateS3Prefix(width, height int) string {
	var aspect string
	switch {
	case width > height:
		aspect = "landscape"
	case height > width:
		aspect = "portrait"
	default:
		aspect = "other"
	}
	return "cloudfront.net/amazonaws.com/" + aspect + "/"
}

func (cfg *apiConfig) handlerUploadVideo(w http.ResponseWriter, r *http.Request) {
	videoIDString := r.PathValue("videoID")
	videoID, err := uuid.Parse(videoIDString)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid ID", err)
		return
	}

	const maxMemory = 10 << 30
	r.ParseMultipartForm(maxMemory)

	// "video" should match the HTML form input name
	file, header, err := r.FormFile("video")
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Unable to parse form file", err)
		return
	}
	defer file.Close()

	mediaType, _, err := mime.ParseMediaType(header.Header.Get("Content-Type"))
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Unable to parse media type", err)
		return
	}

	if mediaType != "video/mp4" {
		respondWithError(w, http.StatusBadRequest, "Unsupported video type", nil)
		return
	}

	video, err := cfg.authenticateAndGetVideoMetadata(w, r, videoID)
	if err != nil {
		return
	}

	ext, err := ExtensionFromContentType(mediaType)
	if err != nil || len(ext) == 0 {
		respondWithError(w, http.StatusBadRequest, "Invalid media type", err)
		return
	}

	randBytes := make([]byte, 32)
	_, err = rand.Read(randBytes)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Could not generate filename", err)
		return
	}
	tempFileName := base64.RawURLEncoding.EncodeToString(randBytes) + ext
	awsKeyBase := tempFileName

	f, err := os.CreateTemp("./tmp", tempFileName)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Could not create temp file", err)
		return
	}
	defer os.Remove(f.Name())
	defer f.Close()

	_, err = io.Copy(f, file)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Could not save file", err)
		return
	}

	videoWidth, videoHeight, err := getVideoAspectRatio(f.Name())
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Could not get video dimensions", err)
		return
	}

	fastStartFilePath, err := processVideoForFastStart(f.Name())
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Could not process video for fast start", err)
		return
	}
	defer os.Remove(fastStartFilePath)

	f, err = os.Open(fastStartFilePath)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Could not open processed video", err)
		return
	}
	defer f.Close()

	awsS3Key := generateS3Prefix(videoWidth, videoHeight) + awsKeyBase

	_, err = f.Seek(0, io.SeekStart)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Could not seek temp file", err)
		return
	}

	_, err = cfg.s3Client.PutObject(context.Background(), &s3.PutObjectInput{
		Bucket:      &cfg.s3Bucket,
		Key:         &awsS3Key,
		Body:        f,
		ContentType: &mediaType,
	})
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Could not upload to S3", err)
		return
	}

	// Pre-signed URL logic: store bucket and key as comma separated values
	// VideoURL := cfg.s3Bucket + "," + awsS3Key
	// video.VideoURL = &VideoURL
	VideoURL := cfg.s3ObjectURL(awsS3Key)
	video.VideoURL = &VideoURL
	err = cfg.db.UpdateVideo(video)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Could not update video metadata", err)
		return
	}

	respondWithJSON(w, http.StatusOK, map[string]string{"message": "Video uploaded successfully"})
}
