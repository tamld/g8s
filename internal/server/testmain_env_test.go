package server

import (
	"fmt"
	"os"
	"testing"

	"github.com/tamld/g8s/internal/testenv"
)

func TestMain(m *testing.M) {
	if err := testenv.ScrubAmbientDelegationEnv(); err != nil {
		fmt.Fprintf(os.Stderr, "TestMain: %v\n", err)
		os.Exit(1)
	}
	os.Exit(m.Run())
}

func TestMain_ScrubsAmbientDelegationEnv(t *testing.T) {
	for _, key := range testenv.AmbientDelegationEnvVars() {
		if got, present := os.LookupEnv(key); present {
			t.Errorf("ambient delegation env %q = %q, want empty", key, got)
		}
	}
}
