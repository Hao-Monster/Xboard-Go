package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Hao-Monster/Xboard-Go/internal/lifecycle"
)

func TestRunRejectsInvalidCommandsBeforeDockerAccess(t *testing.T) {
	tests := []struct {
		name      string
		arguments []string
		message   string
	}{
		{name: "missing", message: "subcommand is required"},
		{name: "unknown", arguments: []string{"destroy"}, message: "unknown lifecycle subcommand"},
		{name: "install image", arguments: []string{"install"}, message: "requires --image"},
		{name: "status image", arguments: []string{"status", "--image", "candidate"}, message: "does not accept --image"},
		{name: "timeout", arguments: []string{"status", "--health-timeout", "0s"}, message: "health-timeout"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var stdout bytes.Buffer
			var stderr bytes.Buffer
			err := run(context.Background(), test.arguments, &stdout, &stderr, time.Now)
			if err == nil || !strings.Contains(err.Error(), test.message) {
				t.Fatalf("run() error = %v", err)
			}
			if stdout.Len() != 0 {
				t.Fatalf("run() stdout = %q", stdout.String())
			}
		})
	}
}

func TestEncodeResultPreservesFailedMachineResultAndExitError(t *testing.T) {
	var output bytes.Buffer
	operationErr := errors.New("upgrade failed after safe automatic rollback")
	result := lifecycle.Result{Status: "failed", Action: "lifecycle.upgrade"}
	if err := encodeResult(&output, result, operationErr); !errors.Is(err, operationErr) {
		t.Fatalf("encodeResult() error = %v", err)
	}
	if !strings.Contains(output.String(), `"status":"failed"`) || !strings.Contains(output.String(), `"action":"lifecycle.upgrade"`) {
		t.Fatalf("encoded result = %q", output.String())
	}
}
