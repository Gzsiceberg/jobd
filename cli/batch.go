package main

import (
	"fmt"
	"strings"
)

type batchResult[T any] struct {
	Succeeded []T `json:"succeeded"`
	Failed    []struct {
		ID    string `json:"id"`
		Error string `json:"error"`
	} `json:"failed"`
}

func (r batchResult[T]) err(action string) error {
	if len(r.Failed) == 0 {
		return nil
	}
	failures := make([]string, 0, len(r.Failed))
	for _, failure := range r.Failed {
		failures = append(failures, fmt.Sprintf("%s: %s", failure.ID, failure.Error))
	}
	return fmt.Errorf("%s: %d succeeded, %d failed: %s", action, len(r.Succeeded), len(r.Failed), strings.Join(failures, "; "))
}
