package meta

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

type UploadedImage struct {
	Hash           string `json:"hash"`
	URL            string `json:"url,omitempty"`
	Name           string `json:"name,omitempty"`
	Width          int    `json:"width,omitempty"`
	Height         int    `json:"height,omitempty"`
	OriginalWidth  int    `json:"original_width,omitempty"`
	OriginalHeight int    `json:"original_height,omitempty"`
	PermalinkURL   string `json:"permalink_url,omitempty"`
	CreatedTime    string `json:"created_time,omitempty"`
}

type ImageUploadResponse struct {
	Images map[string]UploadedImage `json:"images"`
}

type UploadedVideo struct {
	ID      string      `json:"id"`
	Title   string      `json:"title,omitempty"`
	Status  VideoStatus `json:"status,omitempty"`
	Success bool        `json:"success,omitempty"`
}

type VideoUploadOptions struct {
	Title       string
	Description string
	Name        string
}

func (c *Client) UploadImageFile(
	ctx context.Context,
	accessToken string,
	accountID string,
	filePath string,
) (ImageUploadResponse, error) {
	fileInfo, err := os.Stat(filePath)
	if err != nil {
		return ImageUploadResponse{}, fmt.Errorf("meta: stat image: %w", err)
	}
	if !fileInfo.Mode().IsRegular() {
		return ImageUploadResponse{}, errors.New("meta: image path is not a regular file")
	}
	fileName := filepath.Base(filePath)
	contentType := mediaType(fileName, "image/jpeg")
	var response ImageUploadResponse
	err = c.PostMultipartNoRetry(
		ctx,
		"/"+AdAccountNodeID(accountID)+"/adimages",
		accessToken,
		nil,
		nil,
		[]MultipartFile{{
			FieldName:   "filename",
			FileName:    fileName,
			ContentType: contentType,
			Open: func() (io.ReadCloser, error) {
				return os.Open(filePath)
			},
		}},
		&response,
	)
	if err != nil {
		return ImageUploadResponse{}, err
	}
	if len(response.Images) == 0 {
		return ImageUploadResponse{}, &ResponseError{
			Message: "meta: image upload returned a successful response with no images",
		}
	}
	return response, nil
}

func (c *Client) UploadImage(
	ctx context.Context,
	accessToken string,
	accountID string,
	fileName string,
	contentType string,
	open func() (io.ReadCloser, error),
) (ImageUploadResponse, error) {
	if open == nil || strings.TrimSpace(fileName) == "" {
		return ImageUploadResponse{}, errors.New("meta: image file name and opener are required")
	}
	if contentType == "" {
		contentType = mediaType(fileName, "image/jpeg")
	}
	var response ImageUploadResponse
	err := c.PostMultipartNoRetry(
		ctx,
		"/"+AdAccountNodeID(accountID)+"/adimages",
		accessToken,
		nil,
		nil,
		[]MultipartFile{{
			FieldName:   "filename",
			FileName:    filepath.Base(fileName),
			ContentType: contentType,
			Open:        open,
		}},
		&response,
	)
	if err != nil {
		return ImageUploadResponse{}, err
	}
	if len(response.Images) == 0 {
		return ImageUploadResponse{}, &ResponseError{
			Message: "meta: image upload returned a successful response with no images",
		}
	}
	return response, nil
}

// UploadVideoFile performs Meta's direct multipart upload. For very large
// videos callers may use PostMultipart with Meta's resumable upload phases.
func (c *Client) UploadVideoFile(
	ctx context.Context,
	accessToken string,
	accountID string,
	filePath string,
	options VideoUploadOptions,
) (UploadedVideo, error) {
	fileInfo, err := os.Stat(filePath)
	if err != nil {
		return UploadedVideo{}, fmt.Errorf("meta: stat video: %w", err)
	}
	if !fileInfo.Mode().IsRegular() {
		return UploadedVideo{}, errors.New("meta: video path is not a regular file")
	}
	fileName := filepath.Base(filePath)
	if options.Name != "" {
		fileName = filepath.Base(options.Name)
	}
	fields := make(map[string]string)
	if options.Title != "" {
		fields["title"] = options.Title
	}
	if options.Description != "" {
		fields["description"] = options.Description
	}
	var response UploadedVideo
	err = c.PostMultipartNoRetry(
		ctx,
		"/"+AdAccountNodeID(accountID)+"/advideos",
		accessToken,
		nil,
		fields,
		[]MultipartFile{{
			FieldName:   "source",
			FileName:    fileName,
			ContentType: mediaType(fileName, "video/mp4"),
			Open: func() (io.ReadCloser, error) {
				return os.Open(filePath)
			},
		}},
		&response,
	)
	if err != nil {
		return UploadedVideo{}, err
	}
	if response.ID == "" {
		return UploadedVideo{}, &ResponseError{
			Message: "meta: video upload returned a successful response with an empty ID",
		}
	}
	return response, nil
}

func (c *Client) UploadVideo(
	ctx context.Context,
	accessToken string,
	accountID string,
	fileName string,
	contentType string,
	open func() (io.ReadCloser, error),
	options VideoUploadOptions,
) (UploadedVideo, error) {
	if open == nil || strings.TrimSpace(fileName) == "" {
		return UploadedVideo{}, errors.New("meta: video file name and opener are required")
	}
	if contentType == "" {
		contentType = mediaType(fileName, "video/mp4")
	}
	fields := make(map[string]string)
	if options.Title != "" {
		fields["title"] = options.Title
	}
	if options.Description != "" {
		fields["description"] = options.Description
	}
	var response UploadedVideo
	err := c.PostMultipartNoRetry(
		ctx,
		"/"+AdAccountNodeID(accountID)+"/advideos",
		accessToken,
		url.Values{},
		fields,
		[]MultipartFile{{
			FieldName:   "source",
			FileName:    filepath.Base(fileName),
			ContentType: contentType,
			Open:        open,
		}},
		&response,
	)
	if err != nil {
		return UploadedVideo{}, err
	}
	if response.ID == "" {
		return UploadedVideo{}, &ResponseError{
			Message: "meta: video upload returned a successful response with an empty ID",
		}
	}
	return response, nil
}

// FindUploadedVideoByTitle reconciles an /advideos upload whose successful
// response may have been lost. Callers must use a deterministic unique title.
// Meta supports a title filter on this edge, but the result is still matched
// exactly client-side and all returned pages are inspected.
func (c *Client) FindUploadedVideoByTitle(
	ctx context.Context,
	accessToken string,
	accountID string,
	exactTitle string,
) (UploadedVideo, bool, error) {
	exactTitle = strings.TrimSpace(exactTitle)
	if exactTitle == "" {
		return UploadedVideo{}, false, errors.New("meta: video reconciliation title is required")
	}

	videos, err := CollectPages[UploadedVideo](
		ctx,
		c,
		"/"+AdAccountNodeID(accountID)+"/advideos",
		accessToken,
		url.Values{
			"fields": {"id,title,status"},
			"limit":  {"500"},
			"title":  {exactTitle},
		},
	)
	if err != nil {
		return UploadedVideo{}, false, fmt.Errorf("meta: reconcile uploaded video: %w", err)
	}

	var matches []UploadedVideo
	for _, video := range videos {
		if video.Title == exactTitle {
			matches = append(matches, video)
		}
	}
	switch len(matches) {
	case 0:
		return UploadedVideo{}, false, nil
	case 1:
		if strings.TrimSpace(matches[0].ID) == "" {
			return UploadedVideo{}, false, &ResponseError{
				Message: "meta: video reconciliation returned an exact title match with an empty ID",
			}
		}
		return matches[0], true, nil
	default:
		return UploadedVideo{}, false, fmt.Errorf(
			"meta: video reconciliation found %d videos titled %q; refusing an ambiguous retry",
			len(matches),
			exactTitle,
		)
	}
}

func mediaType(fileName, fallback string) string {
	if contentType := mime.TypeByExtension(strings.ToLower(filepath.Ext(fileName))); contentType != "" {
		return contentType
	}
	return fallback
}
