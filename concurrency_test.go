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
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type callbackBean struct {
	contextHook func()
	initHook    func() error
	closeHook   func() error
}

func (b *callbackBean) SetContext(context.Context) {
	if b.contextHook != nil {
		b.contextHook()
	}
}
func (b *callbackBean) PostConstruct() error {
	if b.initHook != nil {
		return b.initHook()
	}
	return nil
}
func (b *callbackBean) Close() error {
	if b.closeHook != nil {
		return b.closeHook()
	}
	return nil
}

func TestCallbacksCanInspectContainer(t *testing.T) {
	defer resetContainer()
	inspect := func() {
		if len(GetBeanScopes()) != 1 || len(GetBeanTypes()) != 0 {
			panic("callbacks did not see the registration snapshot")
		}
		if _, err := RegisterBeanInstance("late", new(string)); err == nil {
			panic("registration allowed during callback")
		}
	}
	_, err := RegisterBeanFactory("bean", Singleton, func(context.Context) (interface{}, error) {
		inspect()
		return &callbackBean{contextHook: inspect, initHook: func() error { inspect(); return nil }, closeHook: func() error { inspect(); return nil }}, nil
	})
	require.NoError(t, err)
	require.NoError(t, RegisterBeanPostprocessor(reflect.TypeOf((*callbackBean)(nil)), func(interface{}) error { inspect(); return nil }))
	done := make(chan error, 1)
	go func() {
		err := InitializeContainer()
		Close()
		done <- err
	}()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(3 * time.Second):
		t.Fatal("callback deadlocked")
	}
}

func TestCloseWaitsForActiveLookup(t *testing.T) {
	defer resetContainer()
	started, releaseFactory := make(chan struct{}), make(chan struct{})
	closed := make(chan struct{})
	_, err := RegisterBeanInstance("singleton", &callbackBean{closeHook: func() error { close(closed); return nil }})
	require.NoError(t, err)
	_, err = RegisterBeanFactory("prototype", Prototype, func(context.Context) (interface{}, error) {
		close(started)
		<-releaseFactory
		return new(string), nil
	})
	require.NoError(t, err)
	require.NoError(t, InitializeContainer())
	lookupDone := make(chan error, 1)
	go func() {
		_, err := GetInstanceSafe("prototype")
		lookupDone <- err
	}()
	<-started
	shutdownDone := make(chan struct{})
	go func() { Close(); close(shutdownDone) }()
	// Rejection of new lookups establishes that shutdown has started.
	require.Eventually(t, func() bool { _, err := GetInstanceSafe("singleton"); return err != nil }, time.Second, time.Millisecond)
	select {
	case <-closed:
		t.Error("singleton closed while a factory was still running")
	default:
	}
	select {
	case <-shutdownDone:
		t.Error("Close returned while a lookup was still running")
	default:
	}
	close(releaseFactory)
	require.NoError(t, <-lookupDone)
	select {
	case <-shutdownDone:
	case <-time.After(time.Second):
		t.Fatal("shutdown did not complete")
	}
	select {
	case <-closed:
	default:
		t.Fatal("singleton was not closed")
	}
}

func TestConcurrentLookupAndClose(t *testing.T) {
	defer resetContainer()
	_, err := RegisterBeanInstance("bean", new(string))
	require.NoError(t, err)
	require.NoError(t, InitializeContainer())
	var workers sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < 8; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			for j := 0; j < 100; j++ {
				_, _ = GetInstanceSafe("bean")
				GetBeanTypes()
				GetBeanScopes()
			}
		}()
	}
	close(start)
	Close()
	workers.Wait()
}

func TestConcurrentCloseWaitsForCleanup(t *testing.T) {
	defer resetContainer()
	started, releaseCloser := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseCloser) }) }
	defer release()
	closed := 0
	_, err := RegisterBeanInstance("bean", &callbackBean{closeHook: func() error {
		close(started)
		<-releaseCloser
		closed++
		return nil
	}})
	require.NoError(t, err)
	require.NoError(t, InitializeContainer())
	firstDone, secondDone := make(chan struct{}), make(chan struct{})
	go func() { Close(); close(firstDone) }()
	<-started
	go func() { Close(); close(secondDone) }()
	select {
	case <-secondDone:
		t.Error("concurrent Close returned before singleton cleanup finished")
	case <-time.After(50 * time.Millisecond):
	}
	release()
	for _, done := range []chan struct{}{firstDone, secondDone} {
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("shutdown did not complete")
		}
	}
	require.Equal(t, 1, closed)
	require.Empty(t, GetBeanScopes())
	_, err = RegisterBeanInstance("new", new(string))
	require.NoError(t, err)
	require.NoError(t, InitializeContainer())
}

func TestRequestCloseErrorDoesNotPanic(t *testing.T) {
	defer resetContainer()
	closed := make(chan struct{})
	_, err := RegisterBeanFactory("request", Request, func(context.Context) (interface{}, error) {
		return &callbackBean{closeHook: func() error { defer close(closed); return errors.New("cleanup failed") }}, nil
	})
	require.NoError(t, err)
	require.NoError(t, InitializeContainer())
	Middleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("request was not cleaned up")
	}
}

func TestLookupRejectsSingletonUnderConstruction(t *testing.T) {
	defer resetContainer()
	started, releaseFactory := make(chan struct{}), make(chan struct{})
	_, err := RegisterBeanFactory("bean", Singleton, func(context.Context) (interface{}, error) {
		close(started)
		<-releaseFactory
		return new(string), nil
	})
	require.NoError(t, err)
	done := make(chan error, 1)
	go func() { done <- InitializeContainer() }()
	<-started
	instance, lookupErr := GetInstanceSafe("bean")
	close(releaseFactory)
	require.NoError(t, <-done)
	require.Nil(t, instance)
	require.EqualError(t, lookupErr, "bean construction is already in progress: bean")
	_, err = GetInstanceSafe("bean")
	require.NoError(t, err)
}

func TestCloseWaitsForInitialization(t *testing.T) {
	defer resetContainer()
	started, releaseHook := make(chan struct{}), make(chan struct{})
	closed := make(chan struct{})
	_, err := RegisterBeanInstance("bean", &callbackBean{
		initHook:  func() error { close(started); <-releaseHook; return nil },
		closeHook: func() error { close(closed); return nil },
	})
	require.NoError(t, err)
	initializationDone := make(chan error, 1)
	go func() { initializationDone <- InitializeContainer() }()
	<-started
	shutdownDone := make(chan struct{})
	go func() { Close(); close(shutdownDone) }()
	select {
	case <-shutdownDone:
		t.Error("shutdown returned before initialization finished")
	case <-time.After(50 * time.Millisecond):
	}
	select {
	case <-closed:
		t.Error("singleton closed while its initialization hook was running")
	default:
	}
	close(releaseHook)
	require.NoError(t, <-initializationDone)
	select {
	case <-shutdownDone:
	case <-time.After(time.Second):
		t.Fatal("shutdown did not resume after initialization")
	}
	select {
	case <-closed:
	default:
		t.Fatal("singleton was not closed")
	}
}

func TestRollbackWaitsForActiveLookup(t *testing.T) {
	defer resetContainer()
	started, releaseFactory := make(chan struct{}), make(chan struct{})
	lookupDone := make(chan error, 1)
	closed := 0
	_, err := RegisterBeanFactory("prototype", Prototype, func(context.Context) (interface{}, error) {
		close(started)
		<-releaseFactory
		return &callbackBean{closeHook: func() error { closed++; return nil }}, nil
	})
	require.NoError(t, err)
	failure := errors.New("startup failed during lookup")
	_, err = RegisterBeanFactory("singleton", Singleton, func(context.Context) (interface{}, error) {
		return &callbackBean{
			initHook: func() error {
				go func() { _, err := GetInstanceSafe("prototype"); lookupDone <- err }()
				<-started
				return failure
			},
			closeHook: func() error { closed++; return nil },
		}, nil
	})
	require.NoError(t, err)
	done := make(chan struct{})
	var initializationErr error
	go func() { initializationErr = InitializeContainer(); close(done) }()
	// Wait until rollback rejects new lookups while the admitted factory is blocked.
	rollingBackStarted := false
	deadline := time.After(time.Second)
	for !rollingBackStarted {
		select {
		case <-deadline:
			close(releaseFactory)
			<-lookupDone
			<-done
			t.Fatal("rollback did not start")
		default:
			initializeShutdownLock.Lock()
			rollingBackStarted = containerState == rollingBack
			initializeShutdownLock.Unlock()
			time.Sleep(time.Millisecond)
		}
	}
	_, err = GetInstanceSafe("singleton")
	require.Error(t, err)
	select {
	case <-done:
		t.Error("rollback returned while a lookup was active")
	default:
	}
	close(releaseFactory)
	require.NoError(t, <-lookupDone)
	<-done
	require.ErrorIs(t, initializationErr, failure)
	require.Equal(t, 2, closed, "rollback must clean up both the singleton and the completed startup lookup")
}

func TestStartupWaitsForParallelSingletonInitialization(t *testing.T) {
	for _, outcome := range []string{"success", "error", "panic"} {
		t.Run(outcome, func(t *testing.T) {
			defer resetContainer()
			started, releaseHook := make(chan struct{}), make(chan struct{})
			lookupDone := make(chan struct{})
			var lookupErr error
			var lookupPanic interface{}
			closed := 0
			failure := errors.New("parallel initialization failed")
			_, err := RegisterBeanFactory("z-worker", Singleton, func(context.Context) (interface{}, error) {
				return &callbackBean{
					initHook: func() error {
						close(started)
						<-releaseHook
						switch outcome {
						case "error":
							return failure
						case "panic":
							panic(failure)
						}
						return nil
					},
					closeHook: func() error { closed++; return nil },
				}, nil
			})
			require.NoError(t, err)
			_, err = RegisterBeanFactory("a-launcher", Singleton, func(context.Context) (interface{}, error) {
				go func() {
					defer func() { lookupPanic = recover(); close(lookupDone) }()
					_, lookupErr = GetInstanceSafe("z-worker")
				}()
				<-started
				return new(string), nil
			})
			require.NoError(t, err)
			done := make(chan struct{})
			var initializationErr error
			go func() { initializationErr = InitializeContainer(); close(done) }()
			<-started
			select {
			case <-done:
				t.Error("startup returned before the parallel singleton hook finished")
			case <-time.After(50 * time.Millisecond):
			}
			close(releaseHook)
			<-lookupDone
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("startup did not finish")
			}
			switch outcome {
			case "success":
				require.NoError(t, initializationErr)
				require.NoError(t, lookupErr)
				require.Nil(t, lookupPanic)
				Close()
			case "error":
				require.ErrorIs(t, initializationErr, failure)
				require.ErrorIs(t, lookupErr, failure)
			case "panic":
				require.Error(t, initializationErr)
				require.Same(t, failure, lookupPanic)
			}
			require.Equal(t, 1, closed)
			_, err = GetInstanceSafe("z-worker")
			require.Error(t, err)
		})
	}
}
