package main

import (
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/auth"
	"github.com/google/uuid"
)

func (cfg *apiConfig) handlerUploadThumbnail(w http.ResponseWriter, r *http.Request) {
	videoIDString := r.PathValue("videoID")
	videoID, err := uuid.Parse(videoIDString)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid ID", err)
		return
	}

	token, err := auth.GetBearerToken(r.Header)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "Couldn't find JWT", err)
		return
	}

	userID, err := auth.ValidateJWT(token, cfg.jwtSecret)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "Couldn't validate JWT", err)
		return
	}

	fmt.Println("uploading thumbnail for video", videoID, "by user", userID)

	// implement the upload
	const maxMemory = 10 << 20
	r.ParseMultipartForm(maxMemory)

	// "thumbnail" should match the HTML form input name
	file, header, err := r.FormFile("thumbnail")
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Unable to parse form file", err)
		return
	}
	headerType := header.Header.Get("Content-Type")
	defer file.Close()

	// Mime the media type to make sure it is correct
	mediaType, _, err := mime.ParseMediaType(headerType)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid media type", err)
		return
	}
	if mediaType != "image/jpeg" && mediaType != "image/png" {
		respondWithError(w, http.StatusBadRequest, "Invalid media type", err)
		return
	}

	// Get video metadata
	video, err := cfg.db.GetVideo(videoID)
	if err != nil {
		respondWithError(w, http.StatusNotFound, "Unable to find video", err)
		return
	}
	if video.UserID != userID {
		respondWithError(w, http.StatusUnauthorized, "User is not the video owner", err)
		return
	}

	// Get paths for the asset
	assetPath := getAssetPath(videoIDString, mediaType)
	if assetPath == "" {
		respondWithError(w, http.StatusBadRequest, "Invalid media type", nil)
		return
	}

	diskPath := cfg.getAssetDiskPath(assetPath)
	assetURL := cfg.getAssetURL(assetPath)

	// create a new file and copy the media to it
	dst, err := os.Create(diskPath)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Unable to create file at diskPath", err)
		return
	}
	defer dst.Close()

	_, err = io.Copy(dst, file)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Unable to copy media to file", err)
		return
	}

	// Update video metadata for new thumbnail URL
	video.ThumbnailURL = &assetURL
	err = cfg.db.UpdateVideo(video)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Unable to update video's thumbnail URL", err)
		return
	}

	respondWithJSON(w, http.StatusOK, video)
}

func getAssetPath(videoID string, mediaType string) string {
	// split media type for ext
	index := strings.LastIndex(mediaType, "/")
	if index == -1 {
		return ""
	}
	ext := mediaType[index+1:]

	assetPath := fmt.Sprintf(
		"/assets/%s.%s",
		videoID,
		ext,
	)

	return assetPath
}

func (cfg *apiConfig) getAssetDiskPath(assetPath string) string {
	path := strings.TrimPrefix(assetPath, "/assets/")
	diskPath := filepath.Join(cfg.assetsRoot, path)
	return diskPath
}

func (cfg *apiConfig) getAssetURL(assetPath string) string {
	assetURL := fmt.Sprintf(
		"http://localhost:%s%s",
		cfg.port,
		assetPath,
	)
	return assetURL
}
