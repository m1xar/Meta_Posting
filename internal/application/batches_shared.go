package application

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/watchers-factory/raze-ads/internal/domain"
	"github.com/watchers-factory/raze-ads/internal/meta"
)

// specForAccount applies a per-ad-account JSON override on top of a batch's
// base specification. The merge runs over generic maps rather than the typed
// struct so an override may touch any field Meta accepts, including ones this
// service does not model yet and carries through RawFields.
//
// T is the publish specification being overridden: meta.HierarchySpec for the
// flat single-campaign path, meta.CampaignTreeSpec for the buyer tree path.
func specForAccount[T any](base T, patch json.RawMessage) (T, error) {
	var zero T
	trimmed := bytes.TrimSpace(patch)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return base, nil
	}
	baseJSON, err := json.Marshal(base)
	if err != nil {
		return zero, err
	}
	var destination map[string]any
	if err := decodeJSONUseNumber(baseJSON, &destination); err != nil {
		return zero, err
	}
	var override map[string]any
	if err := decodeJSONUseNumber(patch, &override); err != nil {
		return zero, err
	}
	deepMerge(destination, override)
	merged, err := json.Marshal(destination)
	if err != nil {
		return zero, err
	}
	var result T
	if err := json.Unmarshal(merged, &result); err != nil {
		return zero, err
	}
	return result, nil
}

// setSpecJSONPointer replaces the value at an RFC 6901 pointer inside spec.
// Media bindings use it to substitute an uploaded image hash or video ID into
// whichever creative field the caller nominated, without this service needing
// to know the shape of that field.
func setSpecJSONPointer[T any](spec *T, pointer string, value string) error {
	encoded, err := json.Marshal(spec)
	if err != nil {
		return err
	}
	var document any
	if err := decodeJSONUseNumber(encoded, &document); err != nil {
		return err
	}
	tokens, err := jsonPointerTokens(pointer)
	if err != nil {
		return err
	}
	if err := setJSONPointerValue(document, tokens, value); err != nil {
		return err
	}
	updated, err := json.Marshal(document)
	if err != nil {
		return err
	}
	return json.Unmarshal(updated, spec)
}

// decodeJSONUseNumber preserves integer precision. Budgets and bid amounts are
// int64 minor units; decoding them through float64 silently rounds large values.
func decodeJSONUseNumber(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	return decoder.Decode(target)
}

func deepMerge(destination, override map[string]any) {
	for key, value := range override {
		overrideMap, overrideIsMap := value.(map[string]any)
		destinationMap, destinationIsMap := destination[key].(map[string]any)
		if overrideIsMap && destinationIsMap {
			deepMerge(destinationMap, overrideMap)
			continue
		}
		destination[key] = value
	}
}

func jsonPointerTokens(pointer string) ([]string, error) {
	if pointer == "" || pointer[0] != '/' {
		return nil, errors.New("target must be a non-empty RFC 6901 JSON pointer")
	}
	raw := strings.Split(pointer[1:], "/")
	for index := range raw {
		raw[index] = strings.ReplaceAll(strings.ReplaceAll(raw[index], "~1", "/"), "~0", "~")
		if raw[index] == "" {
			return nil, errors.New("target contains an empty path segment")
		}
	}
	return raw, nil
}

func setJSONPointerValue(current any, tokens []string, value string) error {
	if len(tokens) == 0 {
		return errors.New("target cannot replace the specification root")
	}
	switch node := current.(type) {
	case map[string]any:
		if len(tokens) == 1 {
			node[tokens[0]] = value
			return nil
		}
		next, ok := node[tokens[0]]
		if !ok {
			return fmt.Errorf("path segment %q does not exist", tokens[0])
		}
		return setJSONPointerValue(next, tokens[1:], value)
	case []any:
		index, err := strconv.Atoi(tokens[0])
		if err != nil || index < 0 || index >= len(node) {
			return fmt.Errorf("invalid array index %q", tokens[0])
		}
		if len(tokens) == 1 {
			node[index] = value
			return nil
		}
		return setJSONPointerValue(node[index], tokens[1:], value)
	default:
		return fmt.Errorf("path crosses scalar at %q", tokens[0])
	}
}

// publishedCheckpoint builds the durable record of one object this service
// created in Meta. It is written before activation, so a crashed or retried
// job can resume from the existing object instead of creating a duplicate.
//
// EffectiveStatus is always PAUSED here because the publisher creates every
// object paused and only activates bottom-up afterwards; DesiredStatus records
// where the object is meant to end up. The flat and tree publish paths differ
// only in how they resolve name, parent and idempotency key, so they resolve
// those and share this body.
func publishedCheckpoint(
	batch *domain.Batch,
	accountResult *domain.BatchAccountResult,
	objectType domain.PublishedObjectType,
	metaObjectID string,
	name string,
	parentMetaObjectID string,
	idempotencyKey string,
	leavePaused bool,
	requestJSON, responseJSON domain.JSON,
) domain.PublishedObject {
	desired := string(meta.StatusActive)
	if leavePaused {
		desired = string(meta.StatusPaused)
	}
	return domain.PublishedObject{
		BatchID:              batch.ID,
		BatchAccountResultID: accountResult.ID,
		ConnectionID:         batch.ConnectionID,
		AdAccountID:          accountResult.AdAccountID,
		ObjectType:           objectType,
		MetaObjectID:         metaObjectID,
		ParentMetaObjectID:   parentMetaObjectID,
		Name:                 name,
		DesiredStatus:        desired,
		EffectiveStatus:      string(meta.StatusPaused),
		IdempotencyKey:       idempotencyKey,
		RequestJSON:          requestJSON,
		ResponseJSON:         responseJSON,
	}
}

// graphErrorCodes renders a Meta Graph error's code and subcode for storage on
// a batch account result. Both publish paths persist them identically, so the
// extraction is shared; the surrounding failure handling is not, and
// deliberately so - see the note on finishPublishFailure.
func graphErrorCodes(graphErr *meta.GraphError) (code string, subcode string) {
	if graphErr == nil {
		return "", ""
	}
	return strconv.Itoa(graphErr.Code), strconv.Itoa(graphErr.ErrorSubcode)
}
