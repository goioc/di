package di

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type lifecycleContextBean struct{ ctx context.Context }

func (b *lifecycleContextBean) SetContext(ctx context.Context) { b.ctx = ctx }
func (b *lifecycleContextBean) PostConstruct() error {
	if b.ctx == nil {
		return errors.New("PostConstruct called before SetContext")
	}
	return nil
}
func TestContextBeforeInit(t *testing.T) {
	for _, scope := range []Scope{Singleton, Prototype, Request} {
		t.Run(string(scope), func(t *testing.T) {
			defer resetContainer()
			_, _ = RegisterBeanFactory("bean", scope, func(context.Context) (any, error) { return &lifecycleContextBean{}, nil })
			err := InitializeContainer()
			if err == nil && scope == Prototype {
				_, err = GetInstanceSafe("bean")
			} else if err == nil && scope == Request {
				getRequestBeanInstance(context.Background(), "bean")
			}
			if err != nil {
				t.Error(err)
			}
		})
	}
}

type lifecycleLookupPrototype struct {
	Scope Scope `di.scope:"prototype"`
}

func (*lifecycleLookupPrototype) PostConstruct() error { _, err := GetInstanceSafe("dep"); return err }

type lifecycleLookupRoot struct {
	Child *lifecycleLookupPrototype `di.inject:"child"`
}

func TestPrototypeStartupLookup(t *testing.T) {
	defer resetContainer()
	_, _ = RegisterBeanInstance("dep", new(string))
	_, _ = RegisterBean("child", reflect.TypeFor[*lifecycleLookupPrototype]())
	_, _ = RegisterBean("root", reflect.TypeFor[*lifecycleLookupRoot]())
	if err := InitializeContainer(); err != nil {
		t.Errorf("injected prototype cannot perform documented PostConstruct lookup: %v", err)
	}
}

type lifecycleInitializedDependency struct{ Ready bool }

func (b *lifecycleInitializedDependency) PostConstruct() error { b.Ready = true; return nil }

type lifecycleInitializationConsumer struct {
	Dependency *lifecycleInitializedDependency `di.inject:"dependency"`
}

func (b *lifecycleInitializationConsumer) PostConstruct() error {
	if !b.Dependency.Ready {
		return errors.New("dependency has not been initialized")
	}
	return nil
}
func TestDependencyInitializationOrder(t *testing.T) {
	defer resetContainer()
	failures := 0
	for range 100 {
		resetContainer()
		_, _ = RegisterBean("consumer", reflect.TypeFor[*lifecycleInitializationConsumer]())
		_, _ = RegisterBean("dependency", reflect.TypeFor[*lifecycleInitializedDependency]())
		if err := InitializeContainer(); err != nil {
			failures++
		}
	}
	if failures > 0 {
		t.Errorf("%d/100 initializations ran consumer before its dependency's PostConstruct", failures)
	}
}

type lifecycleResource struct {
	fail   bool
	closed bool
}

func (b *lifecycleResource) PostConstruct() error {
	if b.fail {
		return errors.New("initialization failed")
	}
	return nil
}
func (b *lifecycleResource) Close() error { b.closed = true; return nil }
func TestRetryResourceLeak(t *testing.T) {
	defer resetContainer()
	var resources []*lifecycleResource
	_, _ = RegisterBeanFactory("resource", Singleton, func(context.Context) (any, error) {
		b := &lifecycleResource{fail: len(resources) == 0}
		resources = append(resources, b)
		return b, nil
	})
	if InitializeContainer() == nil {
		t.Fatal("expected first initialization to fail")
	}
	if err := InitializeContainer(); err != nil {
		t.Fatal(err)
	}
	Close()
	if len(resources) != 2 {
		t.Fatalf("expected two resources, got %d", len(resources))
	}
	if !resources[0].closed {
		t.Error("retry overwrote the failed resource without ever calling Close")
	}
}
func TestFailedRequestCleanup(t *testing.T) {
	defer resetContainer()
	b := &lifecycleResource{fail: true}
	_, _ = RegisterBeanFactory("resource", Request, func(context.Context) (any, error) { return b, nil })
	if err := InitializeContainer(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	func() {
		defer func() { _ = recover() }()
		Middleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil).WithContext(ctx))
	}()
	cancel()
	// A failed bean never reaches middleware's close-goroutine registration.
	if !b.closed {
		t.Error("failed request bean was discarded without Close")
	}
}

func TestFailedRequestCancelsBeforeCleanup(t *testing.T) {
	for _, failureMode := range []string{"initialization error", "initialization panic", "postprocessor error"} {
		t.Run(failureMode, func(t *testing.T) {
			defer resetContainer()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			failure := errors.New(failureMode)
			closed := make(chan struct{})
			_, err := RegisterBeanFactory("request", Request, func(ctx context.Context) (any, error) {
				return &callbackBean{
					initHook: func() error {
						switch failureMode {
						case "initialization error":
							return failure
						case "initialization panic":
							panic(failure)
						}
						return nil
					},
					closeHook: func() error { <-ctx.Done(); close(closed); return nil },
				}, nil
			})
			require.NoError(t, err)
			if failureMode == "postprocessor error" {
				require.NoError(t, RegisterBeanPostprocessor(reflect.TypeFor[*callbackBean](), func(any) error { return failure }))
			}
			require.NoError(t, InitializeContainer())
			done := make(chan any, 1)
			go func() {
				defer func() { done <- recover() }()
				Middleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
					t.Error("handler called despite failed request initialization")
				})).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil).WithContext(ctx))
			}()
			select {
			case recovered := <-done:
				require.Same(t, failure, recovered)
			case <-time.After(time.Second):
				// Release a broken implementation before resetting the shared container.
				cancel()
				<-done
				t.Fatal("failed request cleanup waited for external cancellation")
			}
			select {
			case <-closed:
			default:
				t.Fatal("failed request was not closed")
			}
			require.NoError(t, ctx.Err(), "middleware must not cancel the caller's context")
		})
	}
}

func TestSuccessfulRequestContextLivesUntilHandlerReturns(t *testing.T) {
	defer resetContainer()
	var requestContext context.Context
	closed := make(chan struct{})
	_, err := RegisterBeanFactory("request", Request, func(ctx context.Context) (any, error) {
		requestContext = ctx
		return &callbackBean{closeHook: func() error { <-ctx.Done(); close(closed); return nil }}, nil
	})
	require.NoError(t, err)
	require.NoError(t, InitializeContainer())
	Middleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		require.NoError(t, requestContext.Err(), "request context canceled before handler returned")
	})).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
	require.ErrorIs(t, requestContext.Err(), context.Canceled)
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("successful request was not closed")
	}
}

func TestSingletonFactoryLookup(t *testing.T) {
	defer resetContainer()
	_, _ = RegisterBeanInstance("dep", new(string))
	_, _ = RegisterBeanFactory("bean", Singleton, func(context.Context) (any, error) { return GetInstanceSafe("dep") })
	if err := InitializeContainer(); err != nil {
		t.Errorf("singleton factory cannot lookup registered instance: %v", err)
	}
}
