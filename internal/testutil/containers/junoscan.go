package containers

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"time"

	build "github.com/docker/docker/api/types/build"
	"github.com/docker/go-connections/nat"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

const defaultJunoScanVersion = "v1.4.7-mainnet"

// JunoScanConfig configures a released juno-scan container.
type JunoScanConfig struct {
	// UAHRP is the unified address prefix, for example "jregtest".
	UAHRP string
	// BearerToken enables API bearer auth when set.
	BearerToken string
	// Confirmations is the scanner confirmation depth. Zero keeps 1.
	Confirmations int
}

type JunoScan struct {
	ContainerID string
	URL         string
	Version     string

	c testcontainers.Container
}

// StartJunoScan starts the pinned juno-scan release against jd over the
// Docker bridge network, backed by the embedded rocksdb store.
func StartJunoScan(ctx context.Context, jd *Junocashd, cfg JunoScanConfig) (*JunoScan, error) {
	if jd == nil || jd.c == nil {
		return nil, fmt.Errorf("junoscan: junocashd container is nil")
	}
	nodeIP, err := jd.c.ContainerIP(ctx)
	if err != nil {
		return nil, fmt.Errorf("junoscan: junocashd container ip: %w", err)
	}
	uaHRP := cfg.UAHRP
	if uaHRP == "" {
		uaHRP = "jregtest"
	}
	confirmations := cfg.Confirmations
	if confirmations <= 0 {
		confirmations = 1
	}

	version := defaultJunoScanVersion
	cmd := []string{
		"-listen", "0.0.0.0:8080",
		"-db-driver", "rocksdb",
		"-db-path", "/data/db",
		"-rpc-url", fmt.Sprintf("http://%s:8232", nodeIP),
		"-rpc-user", jd.RPCUser,
		"-rpc-pass", jd.RPCPassword,
		"-ua-hrp", uaHRP,
		"-poll-interval", "100ms",
		"-confirmations", fmt.Sprint(confirmations),
	}
	if cfg.BearerToken != "" {
		cmd = append(cmd, "-api-bearer-token", cfg.BearerToken)
	}

	req := testcontainers.ContainerRequest{
		ImagePlatform: "linux/amd64",
		FromDockerfile: testcontainers.FromDockerfile{
			Context:    junoScanBuildContext(),
			Dockerfile: "Dockerfile",
			BuildArgs: map[string]*string{
				"JUNO_SCAN_VERSION": &version,
			},
			BuildOptionsModifier: func(opts *build.ImageBuildOptions) {
				opts.Platform = "linux/amd64"
				opts.Version = build.BuilderBuildKit
			},
		},
		ExposedPorts: []string{"8080/tcp"},
		Cmd:          cmd,
		// A wrong or missing token gets 401, which still proves the API is up.
		WaitingFor: wait.ForHTTP("/v1/health").
			WithPort(nat.Port("8080/tcp")).
			WithStatusCodeMatcher(func(status int) bool {
				return status == http.StatusOK || status == http.StatusUnauthorized
			}).
			WithStartupTimeout(3 * time.Minute),
	}

	if os.Getenv("JUNO_TEST_LOG") != "" {
		req.FromDockerfile.BuildLogWriter = os.Stdout
		req.LogConsumerCfg = &testcontainers.LogConsumerConfig{
			Consumers: []testcontainers.LogConsumer{&testcontainers.StdoutLogConsumer{}},
		}
	}

	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		return nil, err
	}

	host, err := c.Host(ctx)
	if err != nil {
		_ = c.Terminate(ctx)
		return nil, err
	}
	port, err := c.MappedPort(ctx, nat.Port("8080/tcp"))
	if err != nil {
		_ = c.Terminate(ctx)
		return nil, err
	}

	return &JunoScan{
		ContainerID: c.GetContainerID(),
		URL:         fmt.Sprintf("http://%s:%s", host, port.Port()),
		Version:     version,
		c:           c,
	}, nil
}

func (s *JunoScan) Terminate(ctx context.Context) error {
	if s == nil || s.c == nil {
		return nil
	}
	return s.c.Terminate(ctx)
}

func junoScanBuildContext() string {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return filepath.Join("docker", "juno-scan")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", "..", "docker", "juno-scan"))
}
