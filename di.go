/*
 * Copyright (c) 2021 Go IoC
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
	"github.com/sirupsen/logrus"
	"io"
	"reflect"
	"strconv"
	"sync"
	"sync/atomic"
	"unsafe"
)

// Scope is a enum for bean scopes supported in this IoC container.
type Scope string

const (
	// Singleton is a scope of bean that exists only in one copy in the container and is created at the init-time.
	// If the bean is singleton and implements Close() method, then this method will be called on Close (consumer responsibility to call Close)
	Singleton Scope = "singleton"
	// Prototype is a scope of bean that can exist in multiple copies in the container and is created on demand.
	Prototype Scope = "prototype"
	// Request is a scope of bean whose lifecycle is bound to the web request (or more precisely - to the corresponding
	// context). If the bean implements Close() method, then this method will be called upon corresponding context's
	// cancellation.
	Request Scope = "request"
)

type tag string

const (
	scope    tag = "di.scope"
	inject   tag = "di.inject"
	optional tag = "di.optional"
)

const (
	unsupportedDependencyType        = "unsupported dependency type: all injections must be done by pointer, interface, slice or map"
	beanAlreadyRegistered            = "bean with such ID is already registered, overwriting it"
	requestScopedBeansCantBeInjected = "request-scoped beans can't be injected: they can only be retrieved from the web-context"
)

var initializeShutdownLock sync.Mutex
var createInstanceLock sync.Mutex
var containerInitialized int32
var beans = make(map[string]reflect.Type)
var beanFactories = make(map[string]func(context.Context) (interface{}, error))
var scopes = make(map[string]Scope)
var singletonInstances = make(map[string]interface{})
var userCreatedInstances = make(map[string]bool)
var beanPostprocessors = make(map[reflect.Type][]func(bean interface{}) error)

// InitializingBean is an interface marking beans that need to be additionally initialized after the container is ready.
type InitializingBean interface {
	// PostConstruct method will be called on a bean after the container is initialized.
	PostConstruct() error
}

// ContextAwareBean is an interface marking beans that can accept context. Mostly meant to be used with Request-scoped
// beans (HTTP request context will be propagated for them). For all other beans it's gonna be `context.Background()`.
type ContextAwareBean interface {
	// SetContext method will be called on a bean after its creation.
	SetContext(ctx context.Context)
}

func init() {
	logrus.SetFormatter(&logrus.TextFormatter{})
}

// RegisterBeanPostprocessor function registers postprocessors for beans. Postprocessor is a function that can perform
// some actions on beans after their creation by the container (and self-initialization with PostConstruct).
func RegisterBeanPostprocessor(beanType reflect.Type, postprocessor func(bean interface{}) error) error {
	initializeShutdownLock.Lock()
	defer initializeShutdownLock.Unlock()
	if atomic.CompareAndSwapInt32(&containerInitialized, 1, 1) {
		return errors.New("container is already initialized: can't register bean postprocessor")
	}
	beanPostprocessors[beanType] = append(beanPostprocessors[beanType], postprocessor)
	return nil
}

// InitializeContainer function initializes the IoC container.
func InitializeContainer() error {
	initializeShutdownLock.Lock()
	defer initializeShutdownLock.Unlock()
	if atomic.CompareAndSwapInt32(&containerInitialized, 1, 1) {
		return errors.New("container is already initialized: reinitialization is not supported")
	}
	err := createSingletonInstances()
	if err != nil {
		return err
	}
	err = injectSingletonDependencies()
	if err != nil {
		return err
	}
	atomic.StoreInt32(&containerInitialized, 1)
	err = initializeSingletonInstances()
	if err != nil {
		return err
	}
	return nil
}

// RegisterBean function registers bean by type, the scope of the bean should be defined in the corresponding struct
// using a tag `di.scope` (`Singleton` is used if no scope is explicitly specified). `beanType` should be a reference
// type, e.g.: `reflect.TypeOf((*services.YourService)(nil))`. Return value of `overwritten` is set to `true` if the
// bean with the same `beanID` has been registered already.
func RegisterBean(beanID string, beanType reflect.Type) (overwritten bool, err error) {
	initializeShutdownLock.Lock()
	defer initializeShutdownLock.Unlock()
	if atomic.CompareAndSwapInt32(&containerInitialized, 1, 1) {
		return false, errors.New("container is already initialized: can't register new bean")
	}
	if beanType.Kind() != reflect.Ptr {
		return false, errors.New("bean type must be a pointer")
	}
	var existingBeanType reflect.Type
	var ok bool
	if existingBeanType, ok = beans[beanID]; ok {
		logrus.WithFields(logrus.Fields{
			"id":              beanID,
			"registered bean": existingBeanType,
			"new bean":        beanType,
		}).Warn(beanAlreadyRegistered)
	}
	beanScope, err := getScope(beanType)
	if err != nil {
		return false, err
	}
	beanTypeElement := beanType.Elem()
	for i := 0; i < beanTypeElement.NumField(); i++ {
		field := beanTypeElement.Field(i)
		if _, ok := field.Tag.Lookup(string(inject)); !ok {
			continue
		}
		if field.Type.Kind() != reflect.Ptr && field.Type.Kind() != reflect.Interface &&
			field.Type.Kind() != reflect.Slice && field.Type.Kind() != reflect.Map {
			return false, errors.New(unsupportedDependencyType)
		}
	}
	beans[beanID] = beanType
	scopes[beanID] = *beanScope
	return ok, nil
}

// RegisterBeanInstance function registers bean, provided the pre-created instance of this bean, the scope of such beans
// are always `Singleton`. `beanInstance` can only be a reference or an interface. Return value of `overwritten` is set
// to `true` if the bean with the same `beanID` has been registered already.
func RegisterBeanInstance(beanID string, beanInstance interface{}) (overwritten bool, err error) {
	initializeShutdownLock.Lock()
	defer initializeShutdownLock.Unlock()
	if atomic.CompareAndSwapInt32(&containerInitialized, 1, 1) {
		return false, errors.New("container is already initialized: can't register new bean")
	}
	beanType := reflect.TypeOf(beanInstance)
	if beanType.Kind() != reflect.Ptr {
		return false, errors.New("bean instance must be a pointer")
	}
	var existingBeanType reflect.Type
	var ok bool
	if existingBeanType, ok = beans[beanID]; ok {
		logrus.WithFields(logrus.Fields{
			"id":                beanID,
			"registered bean":   existingBeanType,
			"new bean instance": beanType,
		}).Warn(beanAlreadyRegistered)
	}
	beans[beanID] = beanType
	scopes[beanID] = Singleton
	singletonInstances[beanID] = beanInstance
	userCreatedInstances[beanID] = true
	return ok, nil
}

// RegisterBeanFactory function registers bean, provided the bean factory that will be used by the container in order to
// create an instance of this bean. `beanScope` can be any scope of the supported ones. `beanFactory` can only produce a
// reference or an interface. Return value of `overwritten` is set to `true` if the bean with the same `beanID` has been
// registered already.
func RegisterBeanFactory(beanID string, beanScope Scope, beanFactory func(ctx context.Context) (interface{}, error)) (overwritten bool, err error) {
	initializeShutdownLock.Lock()
	defer initializeShutdownLock.Unlock()
	if atomic.CompareAndSwapInt32(&containerInitialized, 1, 1) {
		return false, errors.New("container is already initialized: can't register new bean factory")
	}
	var existingBeanType reflect.Type
	var ok bool
	if existingBeanType, ok = beans[beanID]; ok {
		logrus.WithFields(logrus.Fields{
			"id":              beanID,
			"registered bean": existingBeanType,
		}).Warn(beanAlreadyRegistered)
	}
	scopes[beanID] = beanScope
	beanFactories[beanID] = beanFactory
	return ok, nil
}

func getScope(bean reflect.Type) (*Scope, error) {
	var beanScope string
	ok := false
	beanElement := bean.Elem()
	for i := 0; i < beanElement.NumField(); i++ {
		field := beanElement.Field(i)
		beanScope, ok = field.Tag.Lookup(string(scope))
		if ok {
			break
		}
	}
	singleton := Singleton
	prototype := Prototype
	request := Request
	if !ok {
		return &singleton, nil
	}
	switch beanScope {
	case string(Singleton):
		return &singleton, nil
	case string(Prototype):
		return &prototype, nil
	case string(Request):
		return &request, nil
	}
	return nil, errors.New("unsupported scope: " + beanScope)
}

func injectSingletonDependencies() error {
	for beanID, instance := range singletonInstances {
		if _, ok := userCreatedInstances[beanID]; ok {
			continue
		}
		if _, ok := beanFactories[beanID]; ok {
			continue
		}
		err := injectDependencies(beanID, instance, make(map[string]int))
		if err != nil {
			return err
		}
	}
	return nil
}

func injectDependencies(beanID string, instance interface{}, chain map[string]int) error {
	logrus.WithField("beanID", beanID).Trace("injecting dependencies")
	instanceType := beans[beanID]
	instanceElement := instanceType.Elem()
	for i := 0; i < instanceElement.NumField(); i++ {
		field := instanceElement.Field(i)
		beanToInject, ok := field.Tag.Lookup(string(inject))
		if !ok {
			continue
		}
		optionalDependency, err := isOptional(field)
		if err != nil {
			return err
		}
		fieldToInject := reflect.ValueOf(instance).Elem().Field(i)
		fieldToInject = reflect.NewAt(fieldToInject.Type(), unsafe.Pointer(fieldToInject.UnsafeAddr())).Elem()
		switch fieldToInject.Kind() {
		case reflect.Ptr, reflect.Interface:
			if beanToInject == "" { // injecting by type, gotta find the candidate first
				candidates := findInjectionCandidates(fieldToInject.Type())
				if len(candidates) < 1 {
					if optionalDependency {
						continue
					} else {
						return errors.New("no candidates found for the injection")
					}
				}
				if len(candidates) > 1 {
					return errors.New("more then one candidate found for the injection")
				}
				beanToInject = candidates[0]
			}
			beanToInjectType := beans[beanToInject]
			logInjection(beanID, instanceElement, beanToInject, beanToInjectType)
			beanScope, beanFound := scopes[beanToInject]
			if !beanFound {
				if optionalDependency {
					logrus.Trace("no dependency found, injecting nil since the dependency marked as optional")
					continue
				} else {
					return errors.New("no dependency found")
				}
			}
			if beanScope == Request {
				return errors.New(requestScopedBeansCantBeInjected)
			}
			instanceToInject, err := getInstance(context.Background(), beanToInject, chain)
			if err != nil {
				return err
			}
			fieldToInject.Set(reflect.ValueOf(instanceToInject))
		case reflect.Slice:
			if fieldToInject.Type().Elem().Kind() != reflect.Ptr && fieldToInject.Type().Elem().Kind() != reflect.Interface {
				return errors.New(unsupportedDependencyType)
			}
			candidates := findInjectionCandidates(fieldToInject.Type().Elem())
			if len(candidates) < 1 {
				if !optionalDependency {
					fieldToInject.Set(reflect.MakeSlice(fieldToInject.Type(), 0, 0))
				}
				continue
			}
			fieldToInject.Set(reflect.MakeSlice(fieldToInject.Type(), len(candidates), len(candidates)))
			for i, beanToInject := range candidates {
				beanToInjectType := beans[beanToInject]
				logInjection(beanID, instanceElement, beanToInject, beanToInjectType)
				if scopes[beanToInject] == Request {
					return errors.New(requestScopedBeansCantBeInjected)
				}
				instanceToInject, err := getInstance(context.Background(), beanToInject, chain)
				if err != nil {
					return err
				}
				fieldToInject.Index(i).Set(reflect.ValueOf(instanceToInject))
			}
		case reflect.Map:
			if fieldToInject.Type().Elem().Kind() != reflect.Ptr && fieldToInject.Type().Elem().Kind() != reflect.Interface {
				return errors.New(unsupportedDependencyType)
			}
			candidates := findInjectionCandidates(fieldToInject.Type().Elem())
			if len(candidates) < 1 {
				if !optionalDependency {
					fieldToInject.Set(reflect.MakeMap(fieldToInject.Type()))
				}
				continue
			}
			fieldToInject.Set(reflect.MakeMap(fieldToInject.Type()))
			for _, beanToInject := range candidates {
				beanToInjectType := beans[beanToInject]
				logInjection(beanID, instanceElement, beanToInject, beanToInjectType)
				if scopes[beanToInject] == Request {
					return errors.New(requestScopedBeansCantBeInjected)
				}
				instanceToInject, err := getInstance(context.Background(), beanToInject, chain)
				if err != nil {
					return err
				}
				fieldToInject.SetMapIndex(reflect.ValueOf(beanToInject), reflect.ValueOf(instanceToInject))
			}
		default:
			return errors.New(unsupportedDependencyType)
		}
	}
	return nil
}

func logInjection(beanID string, instanceElement reflect.Type, beanToInject string, beanToInjectType reflect.Type) {
	logrus.WithFields(logrus.Fields{
		"bean":               beanID,
		"beanType":           instanceElement,
		"dependencyBean":     beanToInject,
		"dependencyBeanType": beanToInjectType,
	}).Trace("processing dependency")
}

func isOptional(field reflect.StructField) (bool, error) {
	optionalTag := field.Tag.Get(string(optional))
	value, err := strconv.ParseBool(optionalTag)
	if optionalTag != "" && err != nil {
		return false, errors.New("invalid di.optional value: " + optionalTag)
	}
	return value, nil
}

func findInjectionCandidates(fieldToInjectType reflect.Type) []string {
	var candidates []string
	for beanID, beanType := range beans {
		if beanType.AssignableTo(fieldToInjectType) {
			candidates = append(candidates, beanID)
		}
	}
	return candidates
}

func createSingletonInstances() error {
	for beanID := range beans {
		if scopes[beanID] != Singleton {
			continue
		}
		if _, ok := userCreatedInstances[beanID]; ok {
			continue
		}
		instance, err := createInstance(context.Background(), beanID)
		if err != nil {
			return err
		}
		singletonInstances[beanID] = instance
		logrus.WithFields(logrus.Fields{
			"beanID": beanID,
			"scope":  scopes[beanID],
		}).Trace("singleton instance created")
	}
	for beanID, beanFactory := range beanFactories {
		if scopes[beanID] != Singleton {
			continue
		}
		beanInstance, err := beanFactory(context.Background())
		if err != nil {
			return err
		}
		if reflect.TypeOf(beanInstance).Kind() != reflect.Ptr {
			return errors.New("bean factory must return pointer")
		}
		singletonInstances[beanID] = beanInstance
		logrus.WithFields(logrus.Fields{
			"beanID": beanID,
			"scope":  scopes[beanID],
		}).Trace("singleton instance created")
	}
	return nil
}

func createInstance(ctx context.Context, beanID string) (interface{}, error) {
	createInstanceLock.Lock()
	defer createInstanceLock.Unlock()
	if beanFactory, ok := beanFactories[beanID]; ok {
		beanInstance, err := beanFactory(ctx)
		if err != nil {
			return nil, err
		}
		if reflect.TypeOf(beanInstance).Kind() != reflect.Ptr {
			return nil, errors.New("bean factory must return pointer")
		}
		return beanInstance, nil
	}
	logrus.WithField("beanID", beanID).Trace("creating instance")
	return reflect.New(beans[beanID].Elem()).Interface(), nil
}

func initializeSingletonInstances() error {
	for beanID, instance := range singletonInstances {
		err := initializeInstance(beanID, instance)
		if err != nil {
			return err
		}
		err = setContext(context.Background(), beanID, instance)
		if err != nil {
			return err
		}
	}
	return nil
}

func initializeInstance(beanID string, instance interface{}) error {
	initializingBean := reflect.TypeOf((*InitializingBean)(nil)).Elem()
	bean := reflect.TypeOf(instance)
	if bean.Implements(initializingBean) {
		initializingMethod, ok := bean.MethodByName(initializingBean.Method(0).Name)
		if !ok {
			return errors.New("unexpected behavior: can't find method PostConstruct() in bean " + bean.String())
		}
		logrus.WithField("beanID", beanID).Trace("initializing bean")
		errorValue := initializingMethod.Func.Call([]reflect.Value{reflect.ValueOf(instance)})[0]
		if !errorValue.IsNil() {
			return errorValue.Elem().Interface().(error)
		}
	}
	if postprocessors, ok := beanPostprocessors[bean]; ok {
		logrus.WithField("beanID", beanID).Trace("postprocessing bean")
		for _, postprocessor := range postprocessors {
			if err := postprocessor(instance); err != nil {
				return err
			}
		}
	}
	return nil
}

func setContext(ctx context.Context, beanID string, instance interface{}) error {
	contextAwareBean := reflect.TypeOf((*ContextAwareBean)(nil)).Elem()
	bean := reflect.TypeOf(instance)
	if bean.Implements(contextAwareBean) {
		setContextMethod, ok := bean.MethodByName(contextAwareBean.Method(0).Name)
		if !ok {
			return errors.New("unexpected behavior: can't find method SetContext() in bean " + bean.String())
		}
		logrus.WithField("beanID", beanID).WithField("context", ctx).Trace("setting context to bean")
		setContextMethod.Func.Call([]reflect.Value{reflect.ValueOf(instance), reflect.ValueOf(ctx)})
	}
	return nil
}

// GetInstance function returns bean instance by its ID. It may panic, so if receiving the error in return is preferred,
// consider using `GetInstanceSafe`.
func GetInstance(beanID string) interface{} {
	beanInstance, err := GetInstanceSafe(beanID)
	if err != nil {
		panic(err)
	}
	return beanInstance
}

// GetInstanceSafe function returns bean instance by its ID. It doesnt panic upon explicit error, but returns the error
// instead.
func GetInstanceSafe(beanID string) (interface{}, error) {
	if atomic.CompareAndSwapInt32(&containerInitialized, 0, 0) {
		return nil, errors.New("container is not initialized: can't lookup instances of beans yet")
	}
	if scopes[beanID] == Request {
		return nil, errors.New("request-scoped beans can't be retrieved directly from the container: they can only be retrieved from the web-context")
	}
	return getInstance(context.Background(), beanID, make(map[string]int))
}

func getRequestBeanInstance(ctx context.Context, beanID string) interface{} {
	if atomic.CompareAndSwapInt32(&containerInitialized, 0, 0) {
		panic("container is not initialized: can't lookup instances of beans yet")
	}
	beanInstance, err := getInstance(ctx, beanID, make(map[string]int))
	if err != nil {
		panic(err)
	}
	return beanInstance
}

func isBeanRegistered(beanID string) bool {
	if _, ok := beans[beanID]; ok {
		return true
	}
	if _, ok := beanFactories[beanID]; ok {
		return true
	}
	return false
}

func getInstance(ctx context.Context, beanID string, chain map[string]int) (interface{}, error) {
	if !isBeanRegistered(beanID) {
		return nil, errors.New("bean is not registered: " + beanID)
	}
	if scopes[beanID] == Singleton {
		return singletonInstances[beanID], nil
	}
	if count := chain[beanID]; count > 1 {
		return nil, errors.New("circular dependency detected for bean: " + beanID)
	}
	chain[beanID]++
	instance, err := createInstance(ctx, beanID)
	if err != nil {
		return nil, err
	}
	if _, ok := beanFactories[beanID]; !ok {
		err := injectDependencies(beanID, instance, chain)
		if err != nil {
			return nil, err
		}
	}
	err = initializeInstance(beanID, instance)
	if err != nil {
		return nil, err
	}
	err = setContext(ctx, beanID, instance)
	if err != nil {
		return nil, err
	}
	return instance, nil
}

// GetBeanTypes returns a map (copy) of beans registered in the Container, omitting bean factories, because their real
// return type is unknown.
func GetBeanTypes() map[string]reflect.Type {
	initializeShutdownLock.Lock()
	defer initializeShutdownLock.Unlock()
	beanTypes := make(map[string]reflect.Type)
	for k, v := range beans {
		beanTypes[k] = v
	}
	return beanTypes
}

// GetBeanScopes returns a map (copy) of bean scopes registered in the Container.
func GetBeanScopes() map[string]Scope {
	initializeShutdownLock.Lock()
	defer initializeShutdownLock.Unlock()
	beanScopes := make(map[string]Scope)
	for k, v := range scopes {
		beanScopes[k] = v
	}
	return beanScopes
}

// Close destroys the IoC container - executes io.Closer for all beans which implements it.
// This is responsibility of consumer to call Close method.
// If io.Closer returns an error it will just log the error and continue to Close other beans.
func Close() {
	initializeShutdownLock.Lock()
	defer initializeShutdownLock.Unlock()

	for key, value := range singletonInstances {
		fnc, ok := value.(io.Closer)
		if ok {
			err := fnc.Close()
			if err != nil {
				logrus.WithField("beanID", key).Error(err)
			}
		}
	}

	resetContainerWithoutLock()
}

func resetContainer() {
	initializeShutdownLock.Lock()
	defer initializeShutdownLock.Unlock()
	resetContainerWithoutLock()
}

func resetContainerWithoutLock() {
	containerInitialized = 0
	beans = make(map[string]reflect.Type)
	beanFactories = make(map[string]func(context.Context) (interface{}, error))
	scopes = make(map[string]Scope)
	singletonInstances = make(map[string]interface{})
	userCreatedInstances = make(map[string]bool)
	beanPostprocessors = make(map[reflect.Type][]func(bean interface{}) error)
}
