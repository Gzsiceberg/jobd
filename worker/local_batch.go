package main

import (
	"fmt"
	"slices"
	"strings"
)

type batchFailure struct {
	ID    string `json:"id"`
	Error string `json:"error"`
}

type batchResult struct {
	Succeeded []string       `json:"succeeded"`
	Failed    []batchFailure `json:"failed"`
}

// Each operation owns its transaction; one invalid target cannot block others.
func (q *localQueue) batchJobs(ids []string, action string) (batchResult, error) {
	urgent := action == "urgent"
	result := batchResult{Succeeded: []string{}, Failed: []batchFailure{}}
	if len(ids) == 0 || len(ids) > 100 {
		return result, fmt.Errorf("provide between 1 and 100 job IDs")
	}
	unique := make([]string, 0, len(ids))
	seen := make(map[string]bool)
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" || len(id) > 4096 {
			return result, fmt.Errorf("job IDs must contain between 1 and 4096 bytes")
		}
		if !seen[id] {
			seen[id] = true
			unique = append(unique, id)
		}
	}
	if urgent {
		slices.Reverse(unique)
	}
	for _, id := range unique {
		var err error
		switch action {
		case "urgent":
			err = q.reorder(id, "")
		case "retry":
			_, err = q.retry(id)
		case "remove":
			err = q.remove(id)
		default:
			return result, fmt.Errorf("unknown batch action %q", action)
		}
		if err != nil {
			result.Failed = append(result.Failed, batchFailure{ID: id, Error: err.Error()})
		} else {
			result.Succeeded = append(result.Succeeded, id)
		}
	}
	if urgent {
		slices.Reverse(result.Succeeded)
		slices.Reverse(result.Failed)
	}
	return result, nil
}
