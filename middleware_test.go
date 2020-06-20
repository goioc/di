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
	"github.com/stretchr/testify/assert"
	"net/http"
	"net/http/httptest"
	"reflect"
)

var closed bool

type requestBean struct {
	Scope Scope `di.scope:"request"`
}

func (rb *requestBean) Close() error {
	closed = true
	return nil
}

func (suite *TestSuite) TestMiddleware() {
	overwritten, err := RegisterBean("requestBean", reflect.TypeOf((*requestBean)(nil)))
	assert.False(suite.T(), overwritten)
	assert.NoError(suite.T(), err)
	err = InitializeContainer()
	assert.NoError(suite.T(), err)
	middleware := Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestBeanInstance, ok := r.Context().Value(BeanKey("requestBean")).(*requestBean)
		assert.True(suite.T(), ok)
		assert.NotNil(suite.T(), requestBeanInstance)
	}))
	server := httptest.NewServer(middleware)
	defer server.Close()
	_, err = http.Get(server.URL)
	assert.NoError(suite.T(), err)
	assert.True(suite.T(), closed)
}
