package database

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/watchers-factory/raze-posting/internal/domain"
)

func TestDeadPublishAccountResultID(t *testing.T) {
	resultID := uuid.New()
	tests := []struct {
		name string
		job  *domain.Job
		id   uuid.UUID
		ok   bool
	}{
		{
			name: "publish payload",
			job: &domain.Job{
				Type:    publishAccountJobType,
				Payload: domain.MustJSON(map[string]any{"result_id": resultID}),
			},
			id: resultID,
			ok: true,
		},
		{
			name: "different job type is not coupled to batch results",
			job: &domain.Job{
				Type:    "collect_insights",
				Payload: domain.MustJSON(map[string]any{"result_id": resultID}),
			},
		},
		{
			name: "missing result id",
			job: &domain.Job{
				Type:    publishAccountJobType,
				Payload: domain.EmptyJSONObject,
			},
		},
		{
			name: "malformed result id",
			job: &domain.Job{
				Type:    publishAccountJobType,
				Payload: domain.JSON(`{"result_id":"not-a-uuid"}`),
			},
		},
		{
			name: "nil job",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			id, ok := deadPublishAccountResultID(test.job)
			require.Equal(t, test.ok, ok)
			require.Equal(t, test.id, id)
		})
	}
}
