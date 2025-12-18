package main

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"os/exec"
	"strings"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/auth"
	"github.com/google/uuid"
)

func (cfg *apiConfig) handlerUploadVideo(w http.ResponseWriter, r *http.Request) {
	// Set upload limit
	r.Body = http.MaxBytesReader(w, r.Body, 1<<30)

	// Extract videoID
	videoIDString := r.PathValue("videoID")
	videoID, err := uuid.Parse(videoIDString)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid ID", err)
		return
	}

	// Authenticate user
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

	// Get video metadata
	video, err := cfg.db.GetVideo(videoID)
	if err != nil {
		respondWithError(w, http.StatusNotFound, "Unable to find video", err)
		return
	}
	if video.UserID != userID {
		respondWithError(w, http.StatusUnauthorized, "User is not the video owner", nil)
		return
	}

	// Parse the uploaded video file
	file, header, err := r.FormFile("video")
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
	if mediaType != "video/mp4" {
		respondWithError(w, http.StatusBadRequest, "Invalid media type", err)
		return
	}

	// Save upload to a temporary file
	tempFile, err := os.CreateTemp("", "tubely-upload.mp4")
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "error creating temp file", err)
		return
	}
	defer os.Remove(tempFile.Name())
	defer tempFile.Close()

	_, err = io.Copy(tempFile, file)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Unable to copy media to file", err)
		return
	}

	// Reset tempFile's pointer
	_, err = tempFile.Seek(0, io.SeekStart)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Unable to reset temp file pointer", err)
		return
	}

	// Process the video and open it
	processedFile, err := processVideoForFastStart(tempFile.Name())
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Unable to process video file", err)
		return
	}
	processed, err := os.Open(processedFile)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Unable to open processed video", err)
		return
	}
	defer processed.Close()
	defer os.Remove(processedFile)

	// Get aspect ratio
	ratio, err := getVideoAspectRatio(processedFile)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "error determining aspect ratio", err)
		return
	}
	ratioKey := "other"
	if ratio == "16:9" {
		ratioKey = "landscape"
	} else if ratio == "9:16" {
		ratioKey = "portrait"
	}

	// Create random 32 byte hex and add media extension and ratio prefix for file key
	rndm := make([]byte, 32)
	_, err = rand.Read(rndm)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "error reading from crypto/rand", err)
		return
	}
	rndmString := base64.RawURLEncoding.EncodeToString(rndm)
	index := strings.LastIndex(mediaType, "/")
	if index == -1 {
		respondWithError(w, http.StatusBadRequest, "Invalid media type", nil)
		return
	}
	ext := mediaType[index+1:]
	keyString := fmt.Sprintf(
		"%s/%s.%s",
		ratioKey,
		rndmString,
		ext,
	)

	// Put the object into the S3 Bucket
	_, err = cfg.s3Client.PutObject(
		r.Context(),
		&s3.PutObjectInput{
			Bucket:      &cfg.s3Bucket,
			Key:         &keyString,
			Body:        processed,
			ContentType: &mediaType,
		},
	)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Unable to upload video", err)
		return
	}

	// Update video url in database
	videoURL := fmt.Sprintf(
		"https://%s.s3.%s.amazonaws.com/%s",
		cfg.s3Bucket,
		cfg.s3Region,
		keyString,
	)
	video.VideoURL = &videoURL
	err = cfg.db.UpdateVideo(video)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Unable to update video URL", err)
		return
	}

	respondWithJSON(w, http.StatusOK, video)
}

func processVideoForFastStart(filePath string) (string, error) {
	// Set output file path
	outPath := filePath + ".processing"

	// Set the command
	cmd := exec.Command(
		"ffmpeg",
		"-i",
		filePath,
		"-c",
		"copy",
		"-movflags",
		"faststart",
		"-f",
		"mp4",
		outPath,
	)

	// Run the command
	if err := cmd.Run(); err != nil {
		return "", err
	}

	return outPath, nil
}
