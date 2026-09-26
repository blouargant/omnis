// Package packaging holds regression tests for the static packaging assets
// under this directory (the .deb/.rpm login-shell profile script, etc.) that
// are otherwise only ever exercised by actually installing a package.
package packaging

import (
	"os"
	"strings"
	"testing"
)

// The shared test deployment must never ship a token in the iapcli config,
// must mount it read-only, must not run as root, must not grant the pod a
// ServiceAccount token, and must keep A2A off.
func TestIaparcTestManifest(t *testing.T) {
	b, err := os.ReadFile("k8s/iaparc-test/omnis.yaml")
	if err != nil {
		t.Fatal(err)
	}
	m := string(b)
	for _, want := range []string{
		"namespace: test-system",
		"OMNIS_IDENTITY_MODE",
		"automountServiceAccountToken: false",
		"runAsNonRoot: true",
		"readOnly: true",
		"iapregistrykey",
		"nginx.ingress.kubernetes.io/auth-url",
		"path: /omnis",
	} {
		if !strings.Contains(m, want) {
			t.Errorf("manifest missing %q", want)
		}
	}
	for _, bad := range []string{"IAPCLI_TOKEN:", "OMNIS_CONFIG_PATH", "OMNIS_USER_ID", "a2a_enabled: true"} {
		if strings.Contains(m, bad) {
			t.Errorf("manifest must not contain %q", bad)
		}
	}
}
