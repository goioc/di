package di

import (
	"context"
	"io"
	"net/http"
)

// BeanKey identifies a request-scoped bean in an HTTP request's context.
// Retrieve a bean registered as "service" with r.Context().Value(BeanKey("service")).
type BeanKey string

// Middleware creates every Request bean in sorted bean-ID order and stores each
// under BeanKey(beanID) in the context passed to next. InitializeContainer must
// succeed before the handler serves requests.
//
// Successful beans implementing io.Closer are closed asynchronously when the
// request context is canceled or next returns; the middleware does not wait for
// these closers. Failed request initialization cancels its context and cleans up
// synchronously. Cleanup errors are logged. Construction and initialization
// errors panic in the handler goroutine, where outer recovery middleware can
// handle them. The caller's original request context is not canceled.
func Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestContext, cancel := context.WithCancel(r.Context())
		defer cancel()
		func() {
			c, release, err := acquireContainer()
			if err != nil {
				panic(err)
			}
			defer release()
			for _, beanID := range c.ids(Request) {
				beanInstance, err := c.resolve(requestContext, beanID)
				if err != nil {
					panic(err)
				}
				requestContext = context.WithValue(requestContext, BeanKey(beanID), beanInstance)
				if isCloseable(beanInstance) {
					go func(ctx context.Context, id string, instance interface{}) {
						<-ctx.Done()
						closeBean(id, instance)
					}(requestContext, beanID, beanInstance)
				}
			}
		}()
		next.ServeHTTP(w, r.WithContext(requestContext))
	})
}

func isCloseable(beanInstance interface{}) bool {
	_, ok := beanInstance.(io.Closer)
	return ok
}
