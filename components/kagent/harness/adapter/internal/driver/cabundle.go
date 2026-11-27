package driver

import (
	"bytes"
	"errors"
	"fmt"
	"os"

	"github.com/MichaelKinsy/pigpen/components/kagent/harness/adapter/internal/fsutil"
)

// SystemCABundles are the usual locations of the image's own trust store.
var SystemCABundles = []string{"/etc/ssl/certs/ca-certificates.crt", "/etc/pki/tls/certs/ca-bundle.crt"}

// caEnvNames are the variables tools read for a CA file. Several of them
// (Python requests, pip, Deno, the AWS SDK) treat the value as a full
// replacement of the system store, so the file they get must hold both.
var caEnvNames = []string{"SSL_CERT_FILE", "REQUESTS_CA_BUNDLE", "CURL_CA_BUNDLE", "GIT_SSL_CAINFO", "AWS_CA_BUNDLE", "PIP_CERT"}

// CombineCABundle writes dest as the system roots followed by the egress trust
// bundle. kagent projects only the egress MITM CA, so a tool that treats
// SSL_CERT_FILE as the whole store would otherwise fail against hosts the
// gateway passes through. It returns "" when there is no egress bundle or no
// system bundle to combine it with.
func CombineCABundle(systemPaths []string, egress, dest string) (string, error) {
	if egress == "" {
		return "", nil
	}
	egressPEM, err := os.ReadFile(egress)
	if err != nil {
		return "", fmt.Errorf("read the egress trust bundle: %w", err)
	}
	for _, path := range systemPaths {
		system, err := os.ReadFile(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return "", fmt.Errorf("read the system trust bundle: %w", err)
		}
		combined := append(append(bytes.TrimRight(system, "\n"), '\n'), egressPEM...)
		if err := fsutil.ReplacePrivateFile(dest, combined); err != nil {
			return "", err
		}
		return dest, nil
	}
	return "", nil
}
