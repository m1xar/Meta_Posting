package meta

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
)

const videoStatusFields = "id,status"

// VideoStatusResult is the status envelope returned by GET /{video-id}.
// Raw preserves the complete response for durable audit/checkpoint storage
// when Meta adds fields that this client does not know yet.
type VideoStatusResult struct {
	ID     string          `json:"id"`
	Status VideoStatus     `json:"status"`
	Raw    json.RawMessage `json:"-"`
}

func (r *VideoStatusResult) UnmarshalJSON(data []byte) error {
	type alias VideoStatusResult
	var decoded alias
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*r = VideoStatusResult(decoded)
	r.Raw = append(r.Raw[:0], data...)
	return nil
}

// VideoStatus mirrors Meta's asynchronous video processing status object.
// video_status is documented as ready, processing, or error; intermediate
// values such as upload_complete are deliberately treated as not ready.
type VideoStatus struct {
	VideoStatus         string     `json:"video_status,omitempty"`
	ProcessingProgress  int        `json:"processing_progress,omitempty"`
	UploadingPhase      VideoPhase `json:"uploading_phase,omitempty"`
	ProcessingPhase     VideoPhase `json:"processing_phase,omitempty"`
	PublishingPhase     VideoPhase `json:"publishing_phase,omitempty"`
	CopyrightCheckPhase VideoPhase `json:"copyright_check_status,omitempty"`
}

type VideoPhase struct {
	Status           string             `json:"status,omitempty"`
	BytesTransferred int64              `json:"bytes_transferred,omitempty"`
	SourceFileSize   int64              `json:"source_file_size,omitempty"`
	PublishStatus    string             `json:"publish_status,omitempty"`
	PublishTime      json.RawMessage    `json:"publish_time,omitempty"`
	Errors           []VideoStatusError `json:"errors,omitempty"`
}

type VideoStatusError struct {
	Code    int64  `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
}

func (r VideoStatusResult) Ready() bool {
	return normalizedVideoState(r.Status.VideoStatus) == "ready"
}

// VideoProcessingPendingError means Meta accepted the upload but did not make
// it ready before the bounded wait ended. It is safe for a durable job to
// retry by polling the same video ID; the upload must not be replayed.
type VideoProcessingPendingError struct {
	VideoID    string
	LastStatus VideoStatusResult
	Cause      error
}

func (e *VideoProcessingPendingError) Error() string {
	if e == nil {
		return "<nil>"
	}
	state := normalizedVideoState(e.LastStatus.Status.VideoStatus)
	if state == "" {
		state = "unknown"
	}
	message := fmt.Sprintf("meta: video %s is still processing (status=%s)", e.VideoID, state)
	if e.Cause != nil {
		message += ": " + e.Cause.Error()
	}
	return message
}

func (e *VideoProcessingPendingError) Retryable() bool {
	return e != nil
}

func (e *VideoProcessingPendingError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

// VideoProcessingError is Meta's explicit terminal video-processing failure.
// A durable retry must not re-poll indefinitely or re-upload the same invalid
// media; the caller should surface the phase errors to the operator.
type VideoProcessingError struct {
	VideoID    string
	Phase      string
	LastStatus VideoStatusResult
	Errors     []VideoStatusError
}

func (e *VideoProcessingError) Error() string {
	if e == nil {
		return "<nil>"
	}
	message := fmt.Sprintf("meta: video %s processing failed", e.VideoID)
	if e.Phase != "" {
		message += " in " + e.Phase + " phase"
	}
	if len(e.Errors) > 0 {
		detail := strings.TrimSpace(e.Errors[0].Message)
		switch {
		case e.Errors[0].Code != 0 && detail != "":
			message += fmt.Sprintf(" (code %d: %s)", e.Errors[0].Code, detail)
		case e.Errors[0].Code != 0:
			message += fmt.Sprintf(" (code %d)", e.Errors[0].Code)
		case detail != "":
			message += ": " + detail
		}
	}
	return truncateString(message, 4096)
}

func (e *VideoProcessingError) Retryable() bool {
	return false
}

// GetVideoStatus performs one status read without waiting.
func (c *Client) GetVideoStatus(
	ctx context.Context,
	accessToken string,
	videoID string,
) (VideoStatusResult, error) {
	videoID, err := normalizedVideoID(videoID)
	if err != nil {
		return VideoStatusResult{}, err
	}
	var result VideoStatusResult
	err = c.Get(
		ctx,
		"/"+videoID,
		accessToken,
		url.Values{"fields": {videoStatusFields}},
		&result,
	)
	if result.ID == "" {
		result.ID = videoID
	}
	return result, err
}

// WaitForVideoReady polls the ID returned by /advideos until Meta reports
// video_status=ready. It always applies the client's processing timeout, while
// also honoring an earlier caller deadline or cancellation.
func (c *Client) WaitForVideoReady(
	ctx context.Context,
	accessToken string,
	videoID string,
) (VideoStatusResult, error) {
	videoID, err := normalizedVideoID(videoID)
	if err != nil {
		return VideoStatusResult{}, err
	}

	waitCtx, cancel := context.WithTimeout(ctx, c.videoProcessingTimeout)
	defer cancel()

	lastStatus := VideoStatusResult{ID: videoID}
	for attempt := 0; ; attempt++ {
		if err := waitCtx.Err(); err != nil {
			return lastStatus, pendingVideoError(videoID, lastStatus, err)
		}

		status, err := c.GetVideoStatus(waitCtx, accessToken, videoID)
		if err != nil {
			if waitErr := waitCtx.Err(); waitErr != nil {
				return lastStatus, pendingVideoError(videoID, lastStatus, waitErr)
			}
			if isVideoStillProcessingGraphError(err) {
				if err := c.sleep(waitCtx, c.videoPollDelay(attempt)); err != nil {
					if waitErr := waitCtx.Err(); waitErr != nil {
						err = waitErr
					}
					return lastStatus, pendingVideoError(videoID, lastStatus, err)
				}
				continue
			}
			return lastStatus, err
		}
		lastStatus = status
		if status.Ready() {
			return status, nil
		}
		if terminalErr := terminalVideoProcessingError(videoID, status); terminalErr != nil {
			return status, terminalErr
		}

		if err := c.sleep(waitCtx, c.videoPollDelay(attempt)); err != nil {
			if waitErr := waitCtx.Err(); waitErr != nil {
				err = waitErr
			}
			return lastStatus, pendingVideoError(videoID, lastStatus, err)
		}
	}
}

func pendingVideoError(
	videoID string,
	lastStatus VideoStatusResult,
	cause error,
) *VideoProcessingPendingError {
	return &VideoProcessingPendingError{
		VideoID:    videoID,
		LastStatus: lastStatus,
		Cause:      cause,
	}
}

func terminalVideoProcessingError(
	videoID string,
	status VideoStatusResult,
) *VideoProcessingError {
	phases := []struct {
		name  string
		phase VideoPhase
	}{
		{name: "uploading", phase: status.Status.UploadingPhase},
		{name: "processing", phase: status.Status.ProcessingPhase},
		{name: "publishing", phase: status.Status.PublishingPhase},
		{name: "copyright_check", phase: status.Status.CopyrightCheckPhase},
	}
	for _, candidate := range phases {
		if videoStateFailed(candidate.phase.Status) {
			return &VideoProcessingError{
				VideoID:    videoID,
				Phase:      candidate.name,
				LastStatus: status,
				Errors:     append([]VideoStatusError(nil), candidate.phase.Errors...),
			}
		}
	}
	if videoStateFailed(status.Status.VideoStatus) {
		return &VideoProcessingError{
			VideoID:    videoID,
			Phase:      "video",
			LastStatus: status,
		}
	}
	return nil
}

func normalizedVideoID(videoID string) (string, error) {
	videoID = strings.TrimPrefix(strings.TrimSpace(videoID), "/")
	if videoID == "" {
		return "", errors.New("meta: video ID is required")
	}
	if strings.Contains(videoID, "/") {
		return "", errors.New("meta: video ID must not contain a path separator")
	}
	return videoID, nil
}

func normalizedVideoState(state string) string {
	return strings.ToLower(strings.TrimSpace(state))
}

func videoStateFailed(state string) bool {
	switch normalizedVideoState(state) {
	case "error", "failed":
		return true
	default:
		return false
	}
}

func isVideoStillProcessingGraphError(err error) bool {
	var graphErr *GraphError
	if !errors.As(err, &graphErr) || graphErr.Code != 100 {
		return false
	}
	message := strings.ToLower(strings.Join(
		[]string{graphErr.Message, graphErr.ErrorUserTitle, graphErr.ErrorUserMsg},
		" ",
	))
	// Keep this deliberately narrow: code 100 normally means an invalid
	// request and must remain terminal. Meta also uses it transiently while an
	// asynchronously uploaded video is not yet queryable.
	return strings.Contains(message, "still processing") ||
		strings.Contains(message, "video is not ready")
}

func (c *Client) videoPollDelay(attempt int) time.Duration {
	delay := c.videoPollBaseDelay
	for range attempt {
		if delay >= c.videoPollMaxDelay {
			return c.videoPollMaxDelay
		}
		if delay > c.videoPollMaxDelay/2 {
			return c.videoPollMaxDelay
		}
		delay *= 2
	}
	if delay > c.videoPollMaxDelay {
		return c.videoPollMaxDelay
	}
	return delay
}
