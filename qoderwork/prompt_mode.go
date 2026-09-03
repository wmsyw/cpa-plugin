package main

import (
	"strings"
	"sync"
)

const (
	promptModeProxy  = "proxy"
	promptModeNative = "native"
)

var promptModeState = struct {
	sync.RWMutex
	value string
}{value: promptModeProxy}

func normalizePromptMode(value string) string {
	if strings.EqualFold(strings.TrimSpace(value), promptModeNative) {
		return promptModeNative
	}
	return promptModeProxy
}

func setPromptMode(value string) {
	promptModeState.Lock()
	promptModeState.value = normalizePromptMode(value)
	promptModeState.Unlock()
}

func loadedPromptMode() string {
	promptModeState.RLock()
	defer promptModeState.RUnlock()
	return promptModeState.value
}
