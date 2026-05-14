package main

import (
	"fmt"
	"mime"
	"net/http"

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
