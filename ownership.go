package main

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"strings"

	"github.com/YoanWai/agent-manager/internal/store"
)

const continuationPrefix = "--manager-continuation="

func readContinuation(args []string) (*store.ManagerRequest, error) {
	var request *store.ManagerRequest
	for _, arg := range args {
		if !strings.HasPrefix(arg, continuationPrefix) {
			continue
		}
		if request != nil {
			return nil, errors.New("duplicate manager continuation")
		}
		raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(arg, continuationPrefix))
		if err != nil {
			return nil, errors.New("invalid manager continuation")
		}
		request = &store.ManagerRequest{}
		if err := json.Unmarshal(raw, request); err != nil {
			return nil, errors.New("invalid manager continuation")
		}
	}
	return request, nil
}

func continuationArgs(request store.ManagerRequest) []string {
	raw, _ := json.Marshal(request)
	return []string{os.Args[0], continuationPrefix + base64.RawURLEncoding.EncodeToString(raw)}
}
