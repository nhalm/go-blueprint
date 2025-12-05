package repository

import (
	"encoding/json"
	"fmt"
)

func marshalToRawMessage(v any) (*json.RawMessage, error) {
	if v == nil {
		return nil, nil
	}

	// Treat empty maps as NULL in database
	if m, ok := v.(map[string]string); ok && len(m) == 0 {
		return nil, nil
	}

	data, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("marshal metadata: %w", err)
	}

	raw := json.RawMessage(data)
	return &raw, nil
}
