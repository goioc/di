package di

import (
	"bytes"
	"context"
	"log"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/stretchr/testify/require"
)

// di must log through its own logger instance and never read from or write
// through the host application's global logging configuration.
func TestDiLoggingDoesNotTouchGlobalSlog(t *testing.T) {
	defer resetContainer()
	require.NotSame(t, slog.Default(), currentLogger())

	var globalOut, standardOut bytes.Buffer
	originalDefault := slog.Default()
	originalOut, originalFlags, originalPrefix := log.Writer(), log.Flags(), log.Prefix()
	hostHandler := slog.NewTextHandler(&globalOut, &slog.HandlerOptions{Level: slog.LevelError})
	hostLogger := slog.New(hostHandler)
	defer func() {
		slog.SetDefault(originalDefault)
		log.SetOutput(originalOut)
		log.SetFlags(originalFlags)
		log.SetPrefix(originalPrefix)
	}()
	slog.SetDefault(hostLogger)
	log.SetOutput(&standardOut)
	log.SetFlags(log.Lshortfile)
	log.SetPrefix("host: ")
	diOut := captureDiLogs(t, nil)

	// Trigger a di warning: registering the same ID twice.
	_, err := RegisterBeanInstance("duplicate", new(string))
	require.NoError(t, err)
	_, err = RegisterBeanInstance("duplicate", new(string))
	require.NoError(t, err)

	require.Empty(t, globalOut.String())
	require.Empty(t, standardOut.String())
	require.Same(t, hostLogger, slog.Default())
	require.Same(t, hostHandler, hostLogger.Handler())
	require.False(t, hostLogger.Enabled(t.Context(), slog.LevelWarn))
	require.True(t, hostLogger.Enabled(t.Context(), slog.LevelError))
	require.Same(t, &standardOut, log.Writer())
	require.Equal(t, log.Lshortfile, log.Flags())
	require.Equal(t, "host: ", log.Prefix())
	require.Contains(t, diOut.String(), beanAlreadyRegistered)
	require.Contains(t, diOut.String(), "level=WARN")
}

type ctxProbe struct {
	Scope Scope `di.scope:"prototype"`
	ctx   context.Context
}

func (p *ctxProbe) SetContext(ctx context.Context) { p.ctx = ctx }

// A request bean's injected prototype dependency must receive a context
// derived from the HTTP request, not context.Background, so request-bound
// work is canceled when the request ends.
func TestRequestBeanPrototypeDependencyReceivesRequestContext(t *testing.T) {
	defer resetContainer()

	type requestBean struct {
		Scope Scope     `di.scope:"request"`
		Dep   *ctxProbe `di.inject:"probe"`
	}
	_, err := RegisterBean("probe", reflect.TypeFor[*ctxProbe]())
	require.NoError(t, err)
	_, err = RegisterBean("request", reflect.TypeFor[*requestBean]())
	require.NoError(t, err)
	require.NoError(t, InitializeContainer())

	var probe *ctxProbe
	handler := Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rb := r.Context().Value(BeanKey("request")).(*requestBean)
		probe = rb.Dep
	}))

	parent := t.Context()
	parent = context.WithValue(parent, BeanKey("marker"), "request-value")
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil).WithContext(parent))

	require.NotNil(t, probe, "request bean's prototype dependency was not injected")
	if probe.ctx == context.Background() {
		t.Fatal("prototype dependency received context.Background instead of the request context")
	}
	if got := probe.ctx.Value(BeanKey("marker")); got != "request-value" {
		t.Errorf("prototype dependency's context lost the request's values: %v", got)
	}
	// The request has ended; the dependency's context must be canceled with it.
	require.ErrorIs(t, probe.ctx.Err(), context.Canceled,
		"prototype dependency's context is not tied to the request lifecycle")
}

type panicCloser struct {
	closed *bool
}

func (b *panicCloser) Close() error {
	*b.closed = true
	panic("closer exploded")
}

type nilPanicCloser struct{}

func (*nilPanicCloser) Close() error {
	panic(nil)
}

type calmCloser struct {
	closed *bool
}

func (b *calmCloser) Close() error {
	*b.closed = true
	return nil
}

// A panicking bean closer must be logged, not allowed to abort the cleanup of
// the remaining beans or to crash the process.
func TestPanicInCloserDoesNotAbortShutdown(t *testing.T) {
	defer resetContainer()

	logBuf := captureDiLogs(t, nil)

	panicked, otherClosed := false, false
	_, err := RegisterBeanInstance("panicky", &panicCloser{closed: &panicked})
	require.NoError(t, err)
	_, err = RegisterBeanInstance("calm", &calmCloser{closed: &otherClosed})
	require.NoError(t, err)
	require.NoError(t, InitializeContainer())

	require.NotPanics(t, func() { Close() })
	require.True(t, panicked, "the panicking bean must still be closed")
	require.True(t, otherClosed, "beans after the panicking one must still be closed")
	require.Contains(t, logBuf.String(), "panic while closing bean: closer exploded")
	require.Contains(t, logBuf.String(), "level=ERROR")
	require.Contains(t, logBuf.String(), "beanID=panicky")
}

func TestNilPanicInCloserIsLogged(t *testing.T) {
	defer resetContainer()

	logBuf := captureDiLogs(t, nil)

	_, err := RegisterBeanInstance("nil-panicky", &nilPanicCloser{})
	require.NoError(t, err)
	require.NotPanics(t, func() { Close() })
	require.Contains(t, logBuf.String(), "panic while closing bean")
	require.Contains(t, logBuf.String(), "level=ERROR")
	require.Contains(t, logBuf.String(), "beanID=nil-panicky")
}
