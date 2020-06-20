/*
 * Copyright (c) 2020 Go IoC
 *
 * Permission is hereby granted, free of charge, to any person obtaining a copy
 * of this software and associated documentation files (the "Software"), to deal
 * in the Software without restriction, including without limitation the rights
 * to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
 * copies of the Software, and to permit persons to whom the Software is
 * furnished to do so, subject to the following conditions:
 *
 * The above copyright notice and this permission notice shall be included in all
 * copies or substantial portions of the Software.
 */

package di

import (
	"context"
	"io"
	"net/http"
)

// BeanKey is as a Context key, because usage of string keys is discouraged (due to obvious reasons).
type BeanKey string

// Middleware is a function that can be used with http routers to perform Request-scoped beans injection into the web
// request context. If such bean implements io.Closer, it will be attempted to close upon corresponding context
// cancellation (but may panic).
func Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		diContext := r.Context()
		for beanID, scope := range scopes {
			if scope != Request {
				continue
			}
			beanInstance := getRequestBeanInstance(beanID)
			diContext = context.WithValue(diContext, BeanKey(beanID), beanInstance)
			if isCloseable(beanInstance) {
				go func(ctx context.Context, beanInstance interface{}) {
					<-ctx.Done()
					err := beanInstance.(io.Closer).Close()
					if err != nil {
						panic(err)
					}
				}(r.Context(), beanInstance)
			}
		}
		next.ServeHTTP(w, r.WithContext(diContext))
	})
}

func isCloseable(beanInstance interface{}) bool {
	_, ok := beanInstance.(io.Closer)
	return ok
}
