package di

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync"
	"time"

	"github.com/stretchr/testify/assert"
)

type singletonBean struct {
}

type requestBean struct {
	Scope  Scope `di.scope:"request"`
	ctx    context.Context
	closed chan struct{}
}

func (rb *requestBean) SetContext(ctx context.Context) {
	rb.ctx = ctx
	rb.closed = make(chan struct{})
}

func (rb *requestBean) Close() error {
	close(rb.closed)
	return nil
}

func (suite *TestSuite) TestMiddleware() {
	created := make(chan *requestBean, 1)
	overwritten, err := RegisterBean("singletonBean", reflect.TypeFor[*singletonBean]())
	assert.False(suite.T(), overwritten)
	assert.NoError(suite.T(), err)
	overwritten, err = RegisterBean("requestBean", reflect.TypeFor[*requestBean]())
	assert.False(suite.T(), overwritten)
	assert.NoError(suite.T(), err)
	err = InitializeContainer()
	assert.NoError(suite.T(), err)
	middleware := Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		singletonBeanInstance, ok := r.Context().Value(BeanKey("singletonBean")).(*requestBean)
		assert.False(suite.T(), ok)
		assert.Nil(suite.T(), singletonBeanInstance)
		requestBeanInstance, ok := r.Context().Value(BeanKey("requestBean")).(*requestBean)
		assert.True(suite.T(), ok)
		assert.NotNil(suite.T(), requestBeanInstance)
		assert.NotEqual(suite.T(), context.Background(), requestBeanInstance.ctx)
		created <- requestBeanInstance
	}))
	server := httptest.NewServer(middleware)
	defer server.Close()
	resp, err := http.Get(server.URL)
	assert.NoError(suite.T(), err)
	if assert.NotNil(suite.T(), resp) {
		assert.NoError(suite.T(), resp.Body.Close())
	}
	select {
	case bean := <-created:
		select {
		case <-bean.closed:
		case <-time.After(time.Second):
			assert.Fail(suite.T(), "request bean was not closed")
		}
	case <-time.After(time.Second):
		assert.Fail(suite.T(), "request bean was not closed")
	}
}

// countingRequestBean records how many times Close is called, so tests can
// verify that request cleanup runs exactly once per bean.
type countingRequestBean struct {
	Scope  Scope `di.scope:"request"`
	mu     sync.Mutex
	closes int
}

func (cb *countingRequestBean) Close() error {
	cb.mu.Lock()
	cb.closes++
	cb.mu.Unlock()
	return nil
}

func (cb *countingRequestBean) closeCount() int {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	return cb.closes
}

func (suite *TestSuite) TestMiddlewareClosesEachCloseableRequestBeanOnce() {
	firstCreated := make(chan *countingRequestBean, 1)
	secondCreated := make(chan *countingRequestBean, 1)
	overwritten, err := RegisterBean("firstRequestBean", reflect.TypeFor[*countingRequestBean]())
	assert.False(suite.T(), overwritten)
	assert.NoError(suite.T(), err)
	overwritten, err = RegisterBean("secondRequestBean", reflect.TypeFor[*countingRequestBean]())
	assert.False(suite.T(), overwritten)
	assert.NoError(suite.T(), err)
	err = InitializeContainer()
	assert.NoError(suite.T(), err)
	middleware := Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		first, ok := r.Context().Value(BeanKey("firstRequestBean")).(*countingRequestBean)
		assert.True(suite.T(), ok)
		assert.NotNil(suite.T(), first)
		second, ok := r.Context().Value(BeanKey("secondRequestBean")).(*countingRequestBean)
		assert.True(suite.T(), ok)
		assert.NotNil(suite.T(), second)
		assert.NotSame(suite.T(), first, second)
		firstCreated <- first
		secondCreated <- second
	}))
	server := httptest.NewServer(middleware)
	defer server.Close()
	resp, err := http.Get(server.URL)
	assert.NoError(suite.T(), err)
	if assert.NotNil(suite.T(), resp) {
		assert.NoError(suite.T(), resp.Body.Close())
	}
	first := <-firstCreated
	second := <-secondCreated
	bothClosed := make(chan struct{})
	go func() {
		for first.closeCount() < 1 || second.closeCount() < 1 {
			time.Sleep(time.Millisecond)
		}
		close(bothClosed)
	}()
	select {
	case <-bothClosed:
	case <-time.After(time.Second):
		assert.Fail(suite.T(), "request beans were not closed")
	}
	assert.Equal(suite.T(), 1, first.closeCount())
	assert.Equal(suite.T(), 1, second.closeCount())
}

func (suite *TestSuite) TestMiddlewareNotInitialized() {
	overwritten, err := RegisterBean("requestBean", reflect.TypeFor[*requestBean]())
	assert.False(suite.T(), overwritten)
	assert.NoError(suite.T(), err)
	middleware := Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestBeanInstance, ok := r.Context().Value(BeanKey("requestBean")).(*requestBean)
		assert.True(suite.T(), ok)
		assert.NotNil(suite.T(), requestBeanInstance)
	}))
	server := httptest.NewServer(middleware)
	defer server.Close()
	resp, err := http.Get(server.URL)
	assert.Error(suite.T(), err)
	assert.Nil(suite.T(), resp)
}
