package di

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
)

// di must log through its own logger instance and never read from or write
// through the host application's global logrus configuration.
func TestDiLoggingDoesNotTouchGlobalLogrus(t *testing.T) {
	defer resetContainer()

	var diOut bytes.Buffer
	originalOut := logger.Out
	logger.SetOutput(&diOut)
	defer logger.SetOutput(originalOut)

	standard := logrus.StandardLogger()
	originalFormatter := standard.Formatter
	originalGlobalOut := standard.Out
	globalUsed := false
	standard.SetFormatter(&sentinelFormatter{used: &globalUsed})
	standard.SetOutput(io.Discard)
	defer func() {
		standard.SetFormatter(originalFormatter)
		standard.SetOutput(originalGlobalOut)
	}()

	// Trigger a di warning: registering the same ID twice.
	_, err := RegisterBeanInstance("duplicate", new(string))
	require.NoError(t, err)
	_, err = RegisterBeanInstance("duplicate", new(string))
	require.NoError(t, err)

	if globalUsed {
		t.Error("di wrote through the global logrus; it must use its own logger instance")
	}
	if !strings.Contains(diOut.String(), beanAlreadyRegistered) {
		t.Errorf("di's duplicate-registration warning missing from its own logger output: %q", diOut.String())
	}
}

type sentinelFormatter struct {
	used *bool
}

func (f *sentinelFormatter) Format(*logrus.Entry) ([]byte, error) {
	*f.used = true
	return nil, nil
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
	_, err := RegisterBean("probe", reflect.TypeOf((*ctxProbe)(nil)))
	require.NoError(t, err)
	_, err = RegisterBean("request", reflect.TypeOf((*requestBean)(nil)))
	require.NoError(t, err)
	require.NoError(t, InitializeContainer())

	var probe *ctxProbe
	handler := Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rb := r.Context().Value(BeanKey("request")).(*requestBean)
		probe = rb.Dep
	}))

	parent, cancel := context.WithCancel(context.Background())
	defer cancel()
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

	var logBuf bytes.Buffer
	originalOut := logger.Out
	logger.SetOutput(&logBuf)
	defer logger.SetOutput(originalOut)

	panicked, otherClosed := false, false
	_, err := RegisterBeanInstance("panicky", &panicCloser{closed: &panicked})
	require.NoError(t, err)
	_, err = RegisterBeanInstance("calm", &calmCloser{closed: &otherClosed})
	require.NoError(t, err)
	require.NoError(t, InitializeContainer())

	require.NotPanics(t, func() { Close() })
	require.True(t, panicked, "the panicking bean must still be closed")
	require.True(t, otherClosed, "beans after the panicking one must still be closed")
	require.Contains(t, logBuf.String(), "panic while closing bean")
}
