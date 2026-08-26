package httpapi

import (
	"net/http"
	"strings"

	"github.com/gofiber/fiber/v3"
	"github.com/watchers-factory/raze-ads/internal/application"
	"github.com/watchers-factory/raze-ads/internal/domain"
	"github.com/watchers-factory/raze-ads/internal/platform/database"
)

func (s *Server) createBatch(c fiber.Ctx) error {
	var request application.CreateBatchRequest
	if err := decodeJSON(c, &request); err != nil {
		return err
	}
	if request.IdempotencyKey == "" {
		request.IdempotencyKey = strings.TrimSpace(c.Get("Idempotency-Key"))
	}
	if request.CreatedBy == "" {
		request.CreatedBy = "internal_api"
	}
	batch, err := s.service.CreateBatch(c.Context(), request)
	if err != nil {
		return err
	}
	return jsonOK(c, http.StatusAccepted, batch)
}

func (s *Server) listBatches(c fiber.Ctx) error {
	limit, offset, err := pageRequest(c)
	if err != nil {
		return err
	}
	connectionID, err := optionalID(c, "connection_id")
	if err != nil {
		return err
	}
	statuses := make([]domain.BatchStatus, 0)
	for _, raw := range splitQuery(c, "statuses") {
		status := domain.BatchStatus(raw)
		switch status {
		case domain.BatchDraft, domain.BatchQueued, domain.BatchRunning, domain.BatchPartiallySucceeded,
			domain.BatchSucceeded, domain.BatchFailed, domain.BatchCancelled:
			statuses = append(statuses, status)
		default:
			return invalidField("statuses", "contains an unsupported batch status")
		}
	}
	page, err := s.service.Repos.Batches.List(c.Context(), database.BatchFilter{
		ConnectionID: connectionID,
		Statuses:     statuses,
		Page:         domain.PageRequest{Limit: limit, Offset: offset},
	})
	if err != nil {
		return err
	}
	return jsonOK(c, http.StatusOK, page)
}

func (s *Server) getBatch(c fiber.Ctx) error {
	id, err := parseID(c.Params("id"), "id")
	if err != nil {
		return err
	}
	item, err := s.service.Repos.Batches.Get(c.Context(), id)
	if err != nil {
		return err
	}
	return jsonOK(c, http.StatusOK, item)
}

func (s *Server) listBatchResults(c fiber.Ctx) error {
	id, err := parseID(c.Params("id"), "id")
	if err != nil {
		return err
	}
	if _, err := s.service.Repos.Batches.Get(c.Context(), id); err != nil {
		return err
	}
	limit, offset, err := pageRequest(c)
	if err != nil {
		return err
	}
	statuses := make([]domain.BatchAccountStatus, 0)
	for _, raw := range splitQuery(c, "statuses") {
		status := domain.BatchAccountStatus(raw)
		switch status {
		case domain.BatchAccountPending, domain.BatchAccountRunning, domain.BatchAccountSucceeded,
			domain.BatchAccountFailed, domain.BatchAccountSkipped:
			statuses = append(statuses, status)
		default:
			return invalidField("statuses", "contains an unsupported account result status")
		}
	}
	page, err := s.service.Repos.Batches.ListAccountResults(c.Context(), database.BatchAccountResultFilter{
		BatchID:  id,
		Statuses: statuses,
		Page:     domain.PageRequest{Limit: limit, Offset: offset},
	})
	if err != nil {
		return err
	}
	return jsonOK(c, http.StatusOK, page)
}

func (s *Server) listPublishedObjects(c fiber.Ctx) error {
	limit, offset, err := pageRequest(c)
	if err != nil {
		return err
	}
	batchID, err := optionalID(c, "batch_id")
	if err != nil {
		return err
	}
	adAccountID, err := optionalID(c, "ad_account_id")
	if err != nil {
		return err
	}
	objectTypes := make([]domain.PublishedObjectType, 0)
	for _, raw := range splitQuery(c, "object_types") {
		value := domain.PublishedObjectType(raw)
		switch value {
		case domain.PublishedCampaign, domain.PublishedAdSet, domain.PublishedCreative, domain.PublishedAd:
			objectTypes = append(objectTypes, value)
		default:
			return invalidField("object_types", "contains an unsupported object type")
		}
	}
	page, err := s.service.Repos.Batches.ListPublishedObjects(c.Context(), database.PublishedObjectFilter{
		BatchID:     batchID,
		AdAccountID: adAccountID,
		ObjectTypes: objectTypes,
		Page:        domain.PageRequest{Limit: limit, Offset: offset},
	})
	if err != nil {
		return err
	}
	return jsonOK(c, http.StatusOK, page)
}

func (s *Server) listInsights(c fiber.Ctx) error {
	limit, offset, err := pageRequest(c)
	if err != nil {
		return err
	}
	connectionID, err := optionalID(c, "connection_id")
	if err != nil {
		return err
	}
	adAccountID, err := optionalID(c, "ad_account_id")
	if err != nil {
		return err
	}
	publishedObjectID, err := optionalID(c, "published_object_id")
	if err != nil {
		return err
	}
	windowStart, err := parseTimeQuery(c, "window_start")
	if err != nil {
		return err
	}
	windowEnd, err := parseTimeQuery(c, "window_end")
	if err != nil {
		return err
	}
	if windowStart != nil && windowEnd != nil && windowStart.After(*windowEnd) {
		return invalidField("window_start", "must not be after window_end")
	}
	var level *domain.InsightLevel
	if raw := strings.TrimSpace(c.Query("level")); raw != "" {
		value := domain.InsightLevel(raw)
		switch value {
		case domain.InsightAccount, domain.InsightCampaign, domain.InsightAdSet, domain.InsightAd:
			level = &value
		default:
			return invalidField("level", "is not a supported insights level")
		}
	}
	page, err := s.service.Repos.Insights.List(c.Context(), database.InsightFilter{
		ConnectionID:      connectionID,
		AdAccountID:       adAccountID,
		PublishedObjectID: publishedObjectID,
		MetaObjectID:      strings.TrimSpace(c.Query("meta_object_id")),
		Level:             level,
		WindowStart:       windowStart,
		WindowEnd:         windowEnd,
		Page:              domain.PageRequest{Limit: limit, Offset: offset},
	})
	if err != nil {
		return err
	}
	return jsonOK(c, http.StatusOK, page)
}

func (s *Server) listJobs(c fiber.Ctx) error {
	limit, offset, err := pageRequest(c)
	if err != nil {
		return err
	}
	connectionID, err := optionalID(c, "connection_id")
	if err != nil {
		return err
	}
	statuses := make([]domain.JobStatus, 0)
	for _, raw := range splitQuery(c, "statuses") {
		value := domain.JobStatus(raw)
		switch value {
		case domain.JobPending, domain.JobRunning, domain.JobSucceeded, domain.JobDead, domain.JobCancelled:
			statuses = append(statuses, value)
		default:
			return invalidField("statuses", "contains an unsupported job status")
		}
	}
	page, err := s.service.Repos.Jobs.List(c.Context(), database.JobFilter{
		ConnectionID: connectionID,
		Types:        splitQuery(c, "types"),
		Statuses:     statuses,
		Page:         domain.PageRequest{Limit: limit, Offset: offset},
	})
	if err != nil {
		return err
	}
	return jsonOK(c, http.StatusOK, page)
}

func (s *Server) getJob(c fiber.Ctx) error {
	id, err := parseID(c.Params("id"), "id")
	if err != nil {
		return err
	}
	item, err := s.service.Repos.Jobs.Get(c.Context(), id)
	if err != nil {
		return err
	}
	return jsonOK(c, http.StatusOK, item)
}
