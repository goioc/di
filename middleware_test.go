package di

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
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
