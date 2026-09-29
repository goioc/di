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
	"errors"
	"reflect"
	"testing"

	"github.com/stretchr/testify/require"
)

type cleanupParent struct {
	Scope Scope         `di.scope:"prototype"`
	Child *callbackBean `di.inject:"child"`
}

func TestFailedResolutionClosesInjectedPrototypes(t *testing.T) {
	defer resetContainer()
	closed := 0
	_, err := RegisterBeanFactory("child", Prototype, func(context.Context) (interface{}, error) {
		return &callbackBean{closeHook: func() error { closed++; return nil }}, nil
	})
	require.NoError(t, err)
	_, err = RegisterBean("parent", reflect.TypeOf((*cleanupParent)(nil)))
	require.NoError(t, err)
	failure := errors.New("postprocessor failed")
	require.NoError(t, RegisterBeanPostprocessor(reflect.TypeOf((*cleanupParent)(nil)), func(interface{}) error { return failure }))
	require.NoError(t, InitializeContainer())
	_, err = GetInstanceSafe("parent")
	require.ErrorIs(t, err, failure)
	require.Equal(t, 1, closed)
}

func TestRollbackPreservesProvidedInstances(t *testing.T) {
	defer resetContainer()
	providedClosed, createdClosed := 0, 0
	provided := &callbackBean{closeHook: func() error { providedClosed++; return nil }}
	_, err := RegisterBeanInstance("provided", provided)
	require.NoError(t, err)
	attempt := 0
	_, err = RegisterBeanFactory("created", Singleton, func(context.Context) (interface{}, error) {
		attempt++
		fail := attempt == 1
		return &callbackBean{
			initHook: func() error {
				if fail {
					return errors.New("temporary failure")
				}
				return nil
			},
			closeHook: func() error { createdClosed++; return errors.New("cleanup failure") },
		}, nil
	})
	require.NoError(t, err)
	require.EqualError(t, InitializeContainer(), "temporary failure")
	require.Equal(t, 1, createdClosed)
	require.Zero(t, providedClosed)
	require.NoError(t, InitializeContainer())
	require.Same(t, provided, GetInstance("provided"))
	Close()
	require.Equal(t, 2, createdClosed)
	require.Equal(t, 1, providedClosed)
}

type orderedDependency struct {
	events      *[]string
	initialized bool
	closed      bool
}

func (b *orderedDependency) PostConstruct() error {
	b.initialized = true
	*b.events = append(*b.events, "dependency initialized")
	return nil
}
func (b *orderedDependency) Close() error {
	b.closed = true
	*b.events = append(*b.events, "dependency closed")
	return nil
}

type orderedConsumer struct {
	Dependency *orderedDependency `di.inject:"dependency"`
}

func (b *orderedConsumer) PostConstruct() error {
	if !b.Dependency.initialized {
		return errors.New("dependency is not ready")
	}
	*b.Dependency.events = append(*b.Dependency.events, "consumer initialized")
	return nil
}
func (b *orderedConsumer) Close() error {
	if b.Dependency.closed {
		return errors.New("dependency closed before its consumer")
	}
	*b.Dependency.events = append(*b.Dependency.events, "consumer closed")
	return nil
}
func TestCloseUsesReverseDependencyOrder(t *testing.T) {
	defer resetContainer()
	var events []string
	_, err := RegisterBeanFactory("dependency", Singleton, func(context.Context) (interface{}, error) {
		return &orderedDependency{events: &events}, nil
	})
	require.NoError(t, err)
	_, err = RegisterBean("consumer", reflect.TypeOf((*orderedConsumer)(nil)))
	require.NoError(t, err)
	require.NoError(t, InitializeContainer())
	Close()
	require.Equal(t, []string{"dependency initialized", "consumer initialized", "consumer closed", "dependency closed"}, events)
}

type singletonCycleA struct {
	B *singletonCycleB `di.inject:"b"`
}
type singletonCycleB struct {
	A *singletonCycleA `di.inject:"a"`
}

func (b *singletonCycleB) PostConstruct() error {
	if b.A.B != b {
		return errors.New("cycle was not fully wired before hooks ran")
	}
	return nil
}

func TestSingletonFieldCyclesRemainSupported(t *testing.T) {
	defer resetContainer()
	_, err := RegisterBean("a", reflect.TypeOf((*singletonCycleA)(nil)))
	require.NoError(t, err)
	_, err = RegisterBean("b", reflect.TypeOf((*singletonCycleB)(nil)))
	require.NoError(t, err)
	require.NoError(t, InitializeContainer())
	a := GetInstance("a").(*singletonCycleA)
	require.Same(t, a, a.B.A)
}

func TestSingletonCanLookupItselfInPostConstruct(t *testing.T) {
	defer resetContainer()
	var self *callbackBean
	_, err := RegisterBeanFactory("self", Singleton, func(context.Context) (interface{}, error) {
		self = &callbackBean{initHook: func() error {
			got, err := GetInstanceSafe("self")
			if err != nil {
				return err
			}
			if got != self {
				return errors.New("self lookup returned a different instance")
			}
			return nil
		}}
		return self, nil
	})
	require.NoError(t, err)
	require.NoError(t, InitializeContainer())
}

func TestInitializationPanicRollsBackAndCanRetry(t *testing.T) {
	defer resetContainer()
	closed := 0
	panicOnce := true
	_, err := RegisterBeanFactory("bean", Singleton, func(context.Context) (interface{}, error) {
		return &callbackBean{
			initHook: func() error {
				if panicOnce {
					panicOnce = false
					panic("hook failed")
				}
				return nil
			},
			closeHook: func() error { closed++; return nil },
		}, nil
	})
	require.NoError(t, err)
	require.PanicsWithValue(t, "hook failed", func() { _ = InitializeContainer() })
	require.Equal(t, 1, closed)
	_, err = GetInstanceSafe("bean")
	require.Error(t, err)
	require.NoError(t, InitializeContainer())
	Close()
	require.Equal(t, 2, closed)
}
