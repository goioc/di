/*
 * Copyright (c) 2024 Go IoC
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
	"reflect"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRegisterBeanRejectsNonStructPointers(t *testing.T) {
	defer resetContainer()
	original := new(string)
	if _, err := RegisterBeanInstance("bean", original); err != nil {
		t.Fatal(err)
	}
	for _, typ := range []reflect.Type{reflect.TypeOf((*string)(nil)), reflect.TypeOf((**int)(nil)), reflect.TypeOf((*[]string)(nil))} {
		t.Run(typ.String(), func(t *testing.T) {
			if overwritten, err := RegisterBean("bean", typ); overwritten || err == nil {
				t.Fatalf("invalid registration returned (%v, %v)", overwritten, err)
			}
		})
	}
	if err := InitializeContainer(); err != nil {
		t.Fatal(err)
	}
	if got := GetInstance("bean"); got != original {
		t.Fatal("invalid registration replaced the original bean")
	}
}

func TestRegisterBeanPostprocessorRejectsNil(t *testing.T) {
	defer resetContainer()
	if err := RegisterBeanPostprocessor(nil, func(interface{}) error { return nil }); err == nil {
		t.Fatal("nil postprocessor type accepted")
	}
	if err := RegisterBeanPostprocessor(reflect.TypeOf((*string)(nil)), nil); err == nil {
		t.Fatal("nil postprocessor accepted")
	}
	if _, err := RegisterBeanInstance("bean", new(string)); err != nil {
		t.Fatal(err)
	}
	if err := InitializeContainer(); err != nil {
		t.Fatal(err)
	}
}

func TestInjectionRejectsUnsupportedFieldTypes(t *testing.T) {
	for _, bean := range []interface{}{
		&struct {
			Value string `di.inject:""`
		}{},
		&struct {
			Values []string `di.inject:""`
		}{},
		&struct {
			Values map[string]string `di.inject:""`
		}{},
	} {
		t.Run(reflect.TypeOf(bean).String(), func(t *testing.T) {
			defer resetContainer()
			_, err := RegisterBean("bean", reflect.TypeOf(bean))
			if err == nil {
				err = InitializeContainer()
			}
			require.EqualError(t, err, unsupportedDependencyType)
		})
	}
}

func TestCollectionInjectionRejectsRequestDependencies(t *testing.T) {
	defer resetContainer()
	_, err := RegisterBean("request", reflect.TypeOf((*requestBean)(nil)))
	require.NoError(t, err)
	_, err = RegisterBean("consumer", reflect.TypeOf(&struct {
		Requests []*requestBean `di.inject:""`
	}{}))
	require.NoError(t, err)
	require.EqualError(t, InitializeContainer(), requestScopedBeansCantBeInjected)
}

func TestInstanceRegistrationReplacesFactory(t *testing.T) {
	defer resetContainer()
	factoryCalled := false
	_, err := RegisterBeanFactory("bean", Singleton, func(context.Context) (interface{}, error) {
		factoryCalled = true
		return new(string), nil
	})
	require.NoError(t, err)
	replacement := new(string)
	overwritten, err := RegisterBeanInstance("bean", replacement)
	require.NoError(t, err)
	require.True(t, overwritten)
	require.NoError(t, InitializeContainer())
	require.Same(t, replacement, GetInstance("bean"))
	require.False(t, factoryCalled)
}

func TestRequestLookupBeforeInitializationFails(t *testing.T) {
	defer resetContainer()
	_, err := RegisterBean("request", reflect.TypeOf((*requestBean)(nil)))
	require.NoError(t, err)
	require.PanicsWithError(t, "container is not initialized: can't lookup instances of beans yet", func() {
		getRequestBeanInstance(context.Background(), "request")
	})
}
