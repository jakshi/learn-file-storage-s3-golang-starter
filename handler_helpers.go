package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"mime"
	"net/http"
	"os/exec"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/auth"
	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/database"
	"github.com/google/uuid"
)

var preferredExt = map[string]string{
	"image/jpeg":       ".jpg",
	"image/png":        ".png",
	"image/gif":        ".gif",
	"image/webp":       ".webp",
	"video/mp4":        ".mp4",
	"application/pdf":  ".pdf",
	"text/plain":       ".txt",
	"text/html":        ".html",
	"application/json": ".json",
}

func ExtensionFromContentType(contentType string) (string, error) {
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return "", err
	}

	if ext, ok := preferredExt[mediaType]; ok {
		return ext, nil
	}

	exts, err := mime.ExtensionsByType(mediaType)
	if err != nil {
		return "", err
	}
	if len(exts) == 0 {
		return "", fmt.Errorf("no extension for content type %q", mediaType)
	}

	return exts[0], nil
}

func (cfg *apiConfig) s3ObjectURL(key string) string {
	if cfg.s3Endpoint != "" {
		return fmt.Sprintf("%s/%s/%s", cfg.s3Endpoint, cfg.s3Bucket, key)
	}
	return fmt.Sprintf("https://%s.s3.%s.amazonaws.com/%s", cfg.s3Bucket, cfg.s3Region, key)
}

func (cfg *apiConfig) authenticateAndGetVideoMetadata(w http.ResponseWriter, r *http.Request, videoID uuid.UUID) (database.Video, error) {
	token, err := auth.GetBearerToken(r.Header)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "Couldn't find JWT", err)
		return database.Video{}, err
	}

	userID, err := auth.ValidateJWT(token, cfg.jwtSecret)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "Couldn't validate JWT", err)
		return database.Video{}, err
	}

	video, err := cfg.db.GetVideo(videoID)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Unable to get video", err)
		return database.Video{}, err
	}

	if video.UserID != userID {
		respondWithError(w, http.StatusForbidden, "You don't have permission", nil)
		return database.Video{}, fmt.Errorf("user %v does not own video %v", userID, videoID)
	}

	return video, nil
}

func getVideoAspectRatio(filePath string) (int, int, error) {
	cmd := exec.Command("ffprobe", "-v", "error", "-print_format", "json", "-show_streams", filePath)
	output, err := cmd.Output()
	if err != nil {
		return 0, 0, err
	}

	type ffprobeOutput struct {
		Streams []struct {
			Width  int `json:"width"`
			Height int `json:"height"`
		} `json:"streams"`
	}

	var result ffprobeOutput
	err = json.Unmarshal(output, &result)
	if err != nil {
		return 0, 0, err
	}

	width := result.Streams[0].Width
	height := result.Streams[0].Height

	return width, height, nil
}

func processVideoForFastStart(filePath string) (string, error) {
	outputPath := filePath + ".faststart"

	var stdout, stderr bytes.Buffer
	cmd := exec.Command("ffmpeg", "-i", filePath, "-c:v", "copy", "-c:a", "copy", "-movflags", "+faststart", "-f", "mp4", outputPath)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err != nil {
		return "", fmt.Errorf("ffmpeg error: %v\nstdout: %s\nstderr: %s", err, stdout.String(), stderr.String())
	}

	return outputPath, nil
}

func generatePresignedURL(s3Client *s3.Client, bucket, key string, expireTime time.Duration) (string, error) {
	presignClient := s3.NewPresignClient(s3Client)
	presignParams := &s3.GetObjectInput{
		Bucket: &bucket,
		Key:    &key,
	}

	presignResult, err := presignClient.PresignGetObject(context.Background(), presignParams, func(po *s3.PresignOptions) {
		po.Expires = expireTime
	})
	if err != nil {
		return "", fmt.Errorf("failed to generate presigned URL: %w", err)
	}

	return presignResult.URL, nil
}

func (cfg *apiConfig) dbVideoToSignedVideo(video database.Video) (database.Video, error) {
	if video.VideoURL == nil {
		return video, nil
	}

	parts := strings.SplitN(*video.VideoURL, ",", 2)
	if len(parts) != 2 {
		return video, fmt.Errorf("invalid video URL format")
	}
	bucket := parts[0]
	key := parts[1]

	signedURL, err := generatePresignedURL(cfg.s3Client, bucket, key, 24*time.Hour)
	if err != nil {
		return video, fmt.Errorf("failed to generate signed URL: %w", err)
	}

	video.VideoURL = &signedURL
	return video, nil
}
