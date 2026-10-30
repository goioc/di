package di

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"reflect"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func captureDiLogs(t *testing.T, options *slog.HandlerOptions) *bytes.Buffer {
	t.Helper()
	var output bytes.Buffer
	originalLogger := configuredLogger.Load()
	SetLogger(slog.New(slog.NewTextHandler(&output, options)))
	t.Cleanup(func() { SetLogger(originalLogger) })
	return &output
}

func TestDiDefaultLogging(t *testing.T) {
	// Run in a fresh process to exercise the package's original logger and stderr.
	const helperEnv = "GOIOC_DI_TEST_DEFAULT_LOGGING"
	if os.Getenv(helperEnv) == "1" {
		defer resetContainer()
		logger := currentLogger()
		require.IsType(t, &slog.TextHandler{}, logger.Handler())
		require.False(t, logger.Enabled(t.Context(), slog.LevelDebug))
		require.True(t, logger.Enabled(t.Context(), slog.LevelInfo))
		require.True(t, logger.Enabled(t.Context(), slog.LevelWarn))
		require.True(t, logger.Enabled(t.Context(), slog.LevelError))
		for range 2 {
			_, err := RegisterBeanInstance("duplicate", new(string))
			require.NoError(t, err)
		}
		closeBean("failing", &callbackBean{closeHook: func() error { return errors.New("cleanup failure") }})
		return
	}

	cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestDiDefaultLogging$")
	cmd.Env = append(os.Environ(), helperEnv+"=1")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdout, err := cmd.Output()
	require.NoError(t, err, "stdout: %s\nstderr: %s", stdout, stderr.String())
	require.NotContains(t, string(stdout), beanAlreadyRegistered)
	require.NotContains(t, string(stdout), "cleanup failure")
	require.Contains(t, stderr.String(), "level=WARN")
	require.Contains(t, stderr.String(), beanAlreadyRegistered)
	require.Contains(t, stderr.String(), "level=ERROR")
	require.Contains(t, stderr.String(), `msg="cleanup failure" beanID=failing`)
}

func TestSetLoggerNilRestoresDefault(t *testing.T) {
	originalLogger := configuredLogger.Load()
	t.Cleanup(func() { SetLogger(originalLogger) })
	customLogger := slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelDebug}))
	SetLogger(customLogger)
	require.Same(t, customLogger, currentLogger())

	SetLogger(nil)
	require.Same(t, defaultLogger, currentLogger())
	require.False(t, currentLogger().Enabled(t.Context(), slog.LevelDebug))
	require.True(t, currentLogger().Enabled(t.Context(), slog.LevelInfo))
}

func TestSetLoggerUsesCustomHandlerAndAttributes(t *testing.T) {
	type consumer struct {
		Dependency *callbackBean `di.inject:"dependency"`
	}
	for _, factory := range []bool{false, true} {
		name := "type"
		if factory {
			name = "factory"
		}
		t.Run(name, func(t *testing.T) {
			defer resetContainer()
			originalLogger := configuredLogger.Load()
			t.Cleanup(func() { SetLogger(originalLogger) })
			var output bytes.Buffer
			handler := slog.NewJSONHandler(&output, &slog.HandlerOptions{Level: slog.LevelDebug})
			customLogger := slog.New(handler).With("component", "di")
			SetLogger(customLogger)

			dependencyType := reflect.TypeFor[*callbackBean]()
			_, err := RegisterBean("dependency", dependencyType)
			require.NoError(t, err)
			if factory {
				_, err = RegisterBeanFactory("dependency", Singleton, func(context.Context) (any, error) {
					return &callbackBean{}, nil
				})
			} else {
				_, err = RegisterBean("dependency", dependencyType)
			}
			require.NoError(t, err)
			_, err = RegisterBean("consumer", reflect.TypeFor[*consumer]())
			require.NoError(t, err)
			require.NoError(t, InitializeContainer())

			var warning, dependency map[string]any
			decoder := json.NewDecoder(&output)
			for {
				var entry map[string]any
				err := decoder.Decode(&entry)
				if errors.Is(err, io.EOF) {
					break
				}
				require.NoError(t, err)
				require.Equal(t, "di", entry["component"])
				switch entry["msg"] {
				case beanAlreadyRegistered:
					warning = entry
				case "processing dependency":
					dependency = entry
				}
			}
			require.NotNil(t, warning)
			require.Equal(t, "WARN", warning["level"])
			require.Equal(t, "dependency", warning["id"])
			require.Equal(t, dependencyType.String(), warning["registered bean"])
			if !factory {
				require.Equal(t, dependencyType.String(), warning["new bean"])
			}
			require.NotNil(t, dependency)
			require.Equal(t, "DEBUG", dependency["level"])
			require.Equal(t, "consumer", dependency["bean"])
			require.Equal(t, reflect.TypeFor[consumer]().String(), dependency["beanType"])
			require.Equal(t, "dependency", dependency["dependencyBean"])
			if factory {
				require.Equal(t, "<nil>", dependency["dependencyBeanType"])
			} else {
				require.Equal(t, dependencyType.String(), dependency["dependencyBeanType"])
			}

			Close()
			require.Same(t, customLogger, currentLogger())
		})
	}
}

func TestSetLoggerDuringLogging(t *testing.T) {
	originalLogger := configuredLogger.Load()
	t.Cleanup(func() { SetLogger(originalLogger) })
	first := slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelDebug}))
	second := slog.New(slog.NewJSONHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelDebug}))

	var workers sync.WaitGroup
	workers.Go(func() {
		for range 1000 {
			SetLogger(first)
			SetLogger(second)
			SetLogger(nil)
		}
	})
	workers.Go(func() {
		for range 1000 {
			logInjection("bean", reflect.TypeFor[callbackBean](), "dependency", reflect.TypeFor[*callbackBean]())
		}
	})
	workers.Wait()
}

func TestRegistrationWarningsPreserveAttributes(t *testing.T) {
	type oldBean struct{}
	type newBean struct{}
	oldType, newType := reflect.TypeFor[*oldBean](), reflect.TypeFor[*newBean]()
	for _, tc := range []struct {
		name     string
		register func() (bool, error)
		newField string
	}{
		{
			name: "type",
			register: func() (bool, error) {
				return RegisterBean("duplicate", newType)
			},
			newField: `"new bean"=` + newType.String(),
		},
		{
			name: "instance",
			register: func() (bool, error) {
				return RegisterBeanInstance("duplicate", new(newBean))
			},
			newField: `"new bean instance"=` + newType.String(),
		},
		{
			name: "factory",
			register: func() (bool, error) {
				return RegisterBeanFactory("duplicate", Singleton, func(context.Context) (any, error) {
					return new(newBean), nil
				})
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			defer resetContainer()
			output := captureDiLogs(t, nil)
			_, err := RegisterBean("duplicate", oldType)
			require.NoError(t, err)
			overwritten, err := tc.register()
			require.NoError(t, err)
			require.True(t, overwritten)
			require.Contains(t, output.String(), "level=WARN")
			require.Contains(t, output.String(), beanAlreadyRegistered)
			require.Contains(t, output.String(), "id=duplicate")
			require.Contains(t, output.String(), `"registered bean"=`+oldType.String())
			if tc.newField != "" {
				require.Contains(t, output.String(), tc.newField)
			}
		})
	}
}

func TestLifecycleLoggingUsesDebug(t *testing.T) {
	type consumer struct {
		Dependency *callbackBean `di.inject:"dependency"`
	}
	for _, level := range []slog.Level{slog.LevelInfo, slog.LevelDebug} {
		t.Run(level.String(), func(t *testing.T) {
			defer resetContainer()
			output := captureDiLogs(t, &slog.HandlerOptions{Level: level})
			_, err := RegisterBean("dependency", reflect.TypeFor[*callbackBean]())
			require.NoError(t, err)
			_, err = RegisterBean("consumer", reflect.TypeFor[*consumer]())
			require.NoError(t, err)
			require.NoError(t, InitializeContainer())

			if level == slog.LevelInfo {
				require.Empty(t, output.String())
				return
			}
			require.Contains(t, output.String(), `level=DEBUG msg="injecting dependencies" beanID=consumer`)
			require.Contains(t, output.String(), `level=DEBUG msg="processing dependency" bean=consumer`)
			require.Contains(t, output.String(), "beanType="+reflect.TypeFor[consumer]().String())
			require.Contains(t, output.String(), "dependencyBean=dependency")
			require.Contains(t, output.String(), "dependencyBeanType="+reflect.TypeFor[*callbackBean]().String())
			require.Contains(t, output.String(), `level=DEBUG msg="initializing bean" beanID=dependency`)
		})
	}
}
