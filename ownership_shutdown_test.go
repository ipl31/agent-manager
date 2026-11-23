package main

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestShutdownNoticeDoesNotAuthorizeExitOrConsumeCompletion(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	shutdown := startRuntimeShutdown(func(context.Context) error { close(entered); <-release; return nil })
	<-entered
	notice := make(chan struct{})
	returned := make(chan bool, 1)
	go func() {
		returned <- awaitRuntimeShutdown(shutdown.done, make(chan struct{}), false, 10*time.Millisecond, func() { close(notice) })
	}()
	<-notice
	select {
	case <-returned:
		t.Fatal("notice deadline authorized non-forced exit")
	default:
	}
	close(release)
	if forced := <-returned; forced {
		t.Fatal("clean join was forced")
	}
	if forced := awaitRuntimeShutdown(shutdown.done, make(chan struct{}), false, time.Second, func() { t.Error("completed shutdown showed notice") }); forced {
		t.Fatal("second observer lost completion")
	}
}

func TestShutdownRetriesRetainedUncertaintyAndExplicitForceIsSeparate(t *testing.T) {
	release := make(chan struct{})
	shutdown := startRuntimeShutdown(func(context.Context) error {
		select {
		case <-release:
			return nil
		default:
			return errors.New("close not confirmed")
		}
	})
	force := make(chan struct{})
	close(force)
	if !awaitRuntimeShutdown(shutdown.done, force, false, time.Second, func() { t.Error("force waited for notice") }) {
		t.Fatal("explicit force ignored")
	}
	close(release)
	select {
	case <-shutdown.done:
	case <-time.After(time.Second):
		t.Fatal("retained resource could not be confirmed on retry")
	}
}
