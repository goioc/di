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
	for _, typ := range []reflect.Type{reflect.TypeFor[*string](), reflect.TypeFor[**int](), reflect.TypeFor[*[]string]()} {
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
	if err := RegisterBeanPostprocessor(nil, func(any) error { return nil }); err == nil {
		t.Fatal("nil postprocessor type accepted")
	}
	if err := RegisterBeanPostprocessor(reflect.TypeFor[*string](), nil); err == nil {
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
	for _, bean := range []any{
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
	_, err := RegisterBean("request", reflect.TypeFor[*requestBean]())
	require.NoError(t, err)
	_, err = RegisterBean("consumer", reflect.TypeFor[*struct {
		Requests []*requestBean "di.inject:\"\""
	}]())
	require.NoError(t, err)
	require.EqualError(t, InitializeContainer(), requestScopedBeansCantBeInjected)
}

func TestInstanceRegistrationReplacesFactory(t *testing.T) {
	defer resetContainer()
	factoryCalled := false
	_, err := RegisterBeanFactory("bean", Singleton, func(context.Context) (any, error) {
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
	_, err := RegisterBean("request", reflect.TypeFor[*requestBean]())
	require.NoError(t, err)
	require.PanicsWithError(t, "container is not initialized: can't lookup instances of beans yet", func() {
		getRequestBeanInstance(context.Background(), "request")
	})
}
