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
	"sort"
	"strconv"
	"sync"
	"unsafe"

	"github.com/sirupsen/logrus"
)

// Scope is an enum for bean scopes supported in this IoC container.
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
var containerState lifecycleState
var runningContainer *container
var activeLookups int
var shutdownDone chan struct{}
var lifecycleChanged = sync.NewCond(&initializeShutdownLock)
var beans = make(map[string]reflect.Type)
var beanFactories = make(map[string]func(context.Context) (interface{}, error))
var scopes = make(map[string]Scope)
var singletonInstances = make(map[string]interface{})
var userCreatedInstances = make(map[string]bool)
var beanPostprocessors = make(map[reflect.Type][]func(bean interface{}) error)

// InitializingBean marks beans that need initialization after dependency injection.
type InitializingBean interface {
	// PostConstruct is called after SetContext and dependency initialization.
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
	if containerState != uninitialized {
		return errors.New("container is already initialized: can't register bean postprocessor")
	}
	if beanType == nil {
		return errors.New("bean postprocessor type must not be nil")
	}
	if postprocessor == nil {
		return errors.New("bean postprocessor must not be nil")
	}
	beanPostprocessors[beanType] = append(beanPostprocessors[beanType], postprocessor)
	return nil
}

// InitializeContainer creates and initializes the registered singletons. Registration is
// frozen until Close. Factories and hooks may look up other beans during startup;
// applications must wait for InitializeContainer to return before serving requests.
func InitializeContainer() (err error) {
	initializeShutdownLock.Lock()
	if containerState != uninitialized {
		initializeShutdownLock.Unlock()
		return errors.New("container is already initialized: reinitialization is not supported")
	}
	c := newContainer()
	runningContainer = c
	containerState = initializing
	initializeShutdownLock.Unlock()

	succeeded := false
	defer func() {
		defer func() {
			initializeShutdownLock.Lock()
			if succeeded {
				containerState = initialized
			} else {
				runningContainer = nil
				containerState = uninitialized
			}
			lifecycleChanged.Broadcast()
			initializeShutdownLock.Unlock()
		}()
		if !succeeded {
			// Stop admitting lookups before rolling back this attempt. Callbacks
			// already resolving a bean retain the same container snapshot.
			initializeShutdownLock.Lock()
			containerState = rollingBack
			for activeLookups != 0 {
				lifecycleChanged.Wait()
			}
			initializeShutdownLock.Unlock()
			c.rollback()
		}
	}()
	for _, beanID := range c.ids(Singleton) {
		if _, err = c.resolve(context.Background(), beanID); err != nil {
			return err
		}
	}
	c.finishInitialization()
	succeeded = true
	return nil
}

// RegisterBean function registers bean by type, the scope of the bean should be defined in the corresponding struct
// using a tag `di.scope` (`Singleton` is used if no scope is explicitly specified). `beanType` must be a pointer to a struct
// type, e.g.: `reflect.TypeOf((*services.YourService)(nil))`. Return value of `overwritten` is set to `true` if the
// bean with the same `beanID` has been registered already.
func RegisterBean(beanID string, beanType reflect.Type) (overwritten bool, err error) {
	initializeShutdownLock.Lock()
	defer initializeShutdownLock.Unlock()
	if containerState != uninitialized {
		return false, errors.New("container is already initialized: can't register new bean")
	}
	overwritten = isBeanRegistered(beanID)
	if beanType == nil || beanType.Kind() != reflect.Ptr {
		return false, errors.New("bean type must be a pointer")
	}
	if beanType.Elem().Kind() != reflect.Struct {
		return false, errors.New("bean type must be a pointer to a struct")
	}
	var existingBeanType reflect.Type
	if existingBeanType, _ = beans[beanID]; existingBeanType != nil {
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
		if field.Type.Kind() == reflect.Map && field.Type.Key().Kind() != reflect.String {
			return false, errors.New(unsupportedDependencyType)
		}
	}
	clearBeanRegistration(beanID)
	beans[beanID] = beanType
	scopes[beanID] = *beanScope
	return overwritten, nil
}

// RegisterBeanInstance function registers bean, provided the pre-created instance of this bean, the scope of such beans
// are always `Singleton`. `beanInstance` can only be a reference or an interface. Return value of `overwritten` is set
// to `true` if the bean with the same `beanID` has been registered already.
func RegisterBeanInstance(beanID string, beanInstance interface{}) (overwritten bool, err error) {
	initializeShutdownLock.Lock()
	defer initializeShutdownLock.Unlock()
	if containerState != uninitialized {
		return false, errors.New("container is already initialized: can't register new bean")
	}
	overwritten = isBeanRegistered(beanID)
	beanType := reflect.TypeOf(beanInstance)
	if beanType == nil || beanType.Kind() != reflect.Ptr || reflect.ValueOf(beanInstance).IsNil() {
		return false, errors.New("bean instance must be a pointer")
	}
	var existingBeanType reflect.Type
	if existingBeanType, _ = beans[beanID]; existingBeanType != nil {
		logrus.WithFields(logrus.Fields{
			"id":                beanID,
			"registered bean":   existingBeanType,
			"new bean instance": beanType,
		}).Warn(beanAlreadyRegistered)
	}
	clearBeanRegistration(beanID)
	beans[beanID] = beanType
	scopes[beanID] = Singleton
	singletonInstances[beanID] = beanInstance
	userCreatedInstances[beanID] = true
	return overwritten, nil
}

// RegisterBeanFactory function registers bean, provided the bean factory that will be used by the container in order to
// create an instance of this bean. `beanScope` can be any scope of the supported ones. `beanFactory` can only produce a
// reference or an interface. Return value of `overwritten` is set to `true` if the bean with the same `beanID` has been
// registered already.
func RegisterBeanFactory(beanID string, beanScope Scope, beanFactory func(ctx context.Context) (interface{}, error)) (overwritten bool, err error) {
	initializeShutdownLock.Lock()
	defer initializeShutdownLock.Unlock()
	if containerState != uninitialized {
		return false, errors.New("container is already initialized: can't register new bean factory")
	}
	if beanScope != Singleton && beanScope != Prototype && beanScope != Request {
		return false, errors.New("unsupported scope: " + string(beanScope))
	}
	if beanFactory == nil {
		return false, errors.New("bean factory must not be nil")
	}
	overwritten = isBeanRegistered(beanID)
	var existingBeanType reflect.Type
	if existingBeanType, _ = beans[beanID]; existingBeanType != nil {
		logrus.WithFields(logrus.Fields{
			"id":              beanID,
			"registered bean": existingBeanType,
		}).Warn(beanAlreadyRegistered)
	}
	clearBeanRegistration(beanID)
	scopes[beanID] = beanScope
	beanFactories[beanID] = beanFactory
	return overwritten, nil
}

func clearBeanRegistration(beanID string) {
	delete(beans, beanID)
	delete(beanFactories, beanID)
	delete(scopes, beanID)
	delete(singletonInstances, beanID)
	delete(userCreatedInstances, beanID)
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

func (c *container) injectDependencies(beanID string, instance interface{}, resolve func(string) (interface{}, error)) error {
	logrus.WithField("beanID", beanID).Trace("injecting dependencies")
	instanceElement := c.beans[beanID].Elem()
	for i := 0; i < instanceElement.NumField(); i++ {
		field := instanceElement.Field(i)
		dependency, ok := field.Tag.Lookup(string(inject))
		if !ok {
			continue
		}
		optionalDependency, err := isOptional(field)
		if err != nil {
			return err
		}
		target := reflect.ValueOf(instance).Elem().Field(i)
		target = reflect.NewAt(target.Type(), unsafe.Pointer(target.UnsafeAddr())).Elem()
		switch target.Kind() {
		case reflect.Ptr, reflect.Interface:
			err = c.injectField(beanID, dependency, target, optionalDependency, resolve)
		case reflect.Slice, reflect.Map:
			err = c.injectCollection(beanID, target, optionalDependency, resolve)
		default:
			err = errors.New(unsupportedDependencyType)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func (c *container) injectField(beanID, dependency string, target reflect.Value, optionalDependency bool, resolve func(string) (interface{}, error)) error {
	if dependency == "" {
		candidates := c.findInjectionCandidates(target.Type())
		if len(candidates) == 0 {
			if optionalDependency {
				return nil
			}
			return errors.New("no candidates found for the injection")
		}
		if len(candidates) > 1 {
			return errors.New("more then one candidate found for the injection")
		}
		dependency = candidates[0]
	}
	value, err := c.dependencyValue(beanID, dependency, optionalDependency, resolve)
	if err != nil || !value.IsValid() {
		return err
	}
	if !value.Type().AssignableTo(target.Type()) {
		return errors.New("bean is not assignable to dependency field")
	}
	target.Set(value)
	return nil
}

func (c *container) injectCollection(beanID string, target reflect.Value, optionalDependency bool, resolve func(string) (interface{}, error)) error {
	elementType := target.Type().Elem()
	if elementType.Kind() != reflect.Ptr && elementType.Kind() != reflect.Interface {
		return errors.New(unsupportedDependencyType)
	}
	candidates := c.findInjectionCandidates(elementType)
	if len(candidates) == 0 && optionalDependency {
		return nil
	}
	isMap := target.Kind() == reflect.Map
	if isMap {
		target.Set(reflect.MakeMap(target.Type()))
	} else {
		target.Set(reflect.MakeSlice(target.Type(), len(candidates), len(candidates)))
	}
	for i, dependency := range candidates {
		value, err := c.dependencyValue(beanID, dependency, false, resolve)
		if err != nil {
			return err
		}
		if isMap {
			key := reflect.ValueOf(dependency).Convert(target.Type().Key())
			target.SetMapIndex(key, value)
		} else {
			target.Index(i).Set(value)
		}
	}
	return nil
}

func (c *container) dependencyValue(beanID, dependency string, optionalDependency bool, resolve func(string) (interface{}, error)) (reflect.Value, error) {
	beanScope, found := c.scopes[dependency]
	if !found {
		if optionalDependency {
			return reflect.Value{}, nil
		}
		return reflect.Value{}, errors.New("no dependency found")
	}
	if beanScope == Request {
		return reflect.Value{}, errors.New(requestScopedBeansCantBeInjected)
	}
	logInjection(beanID, c.beans[beanID].Elem(), dependency, c.beans[dependency])
	instance, err := resolve(dependency)
	if err != nil {
		return reflect.Value{}, err
	}
	return reflect.ValueOf(instance), nil
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

func (c *container) findInjectionCandidates(fieldToInjectType reflect.Type) []string {
	var candidates []string
	for beanID, beanType := range c.beans {
		if beanType.AssignableTo(fieldToInjectType) {
			candidates = append(candidates, beanID)
		}
	}
	sort.Strings(candidates)
	return candidates
}

func validateFactoryInstance(beanInstance interface{}) error {
	beanType := reflect.TypeOf(beanInstance)
	if beanType == nil || beanType.Kind() == reflect.Ptr && reflect.ValueOf(beanInstance).IsNil() {
		return errors.New("bean factory must return a non-nil pointer")
	}
	if beanType.Kind() != reflect.Ptr {
		return errors.New("bean factory must return pointer")
	}
	return nil
}

func (c *container) initializeInstance(ctx context.Context, beanID string, instance interface{}) error {
	if impl, ok := instance.(ContextAwareBean); ok {
		impl.SetContext(ctx)
	}
	if impl, ok := instance.(InitializingBean); ok {
		logrus.WithField("beanID", beanID).Trace("initializing bean")
		if err := impl.PostConstruct(); err != nil {
			return err
		}
	}
	for _, postprocessor := range c.postprocessors[reflect.TypeOf(instance)] {
		if err := postprocessor(instance); err != nil {
			return err
		}
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
	c, release, err := acquireContainer()
	if err != nil {
		return nil, err
	}
	defer release()
	if c.scopes[beanID] == Request {
		return nil, errors.New("request-scoped beans can't be retrieved directly from the container: they can only be retrieved from the web-context")
	}
	return c.resolve(context.Background(), beanID)
}

func getRequestBeanInstance(ctx context.Context, beanID string) interface{} {
	c, release, err := acquireContainer()
	if err != nil {
		panic(err)
	}
	defer release()
	instance, err := c.resolve(ctx, beanID)
	if err != nil {
		panic(err)
	}
	return instance
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

// Close stops new lookups, waits for active lookups, and closes singletons in
// reverse initialization order. Close errors are logged and cleanup continues.
// Concurrent callers wait for the same shutdown and container reset to finish.
// Stop application work before calling Close: returned beans can outlive a lookup.
// Do not call Close from a factory, initialization callback, or bean closer, or
// wait for Close inside one: shutdown must wait for that callback to finish.
func Close() {
	initializeShutdownLock.Lock()
	for containerState == initializing || containerState == rollingBack {
		lifecycleChanged.Wait()
	}
	if containerState == closing {
		done := shutdownDone
		initializeShutdownLock.Unlock()
		<-done
		return
	}
	c := runningContainer
	if c == nil {
		c = newContainer()
	}
	containerState = closing
	done := make(chan struct{})
	shutdownDone = done
	for activeLookups != 0 {
		lifecycleChanged.Wait()
	}
	initializeShutdownLock.Unlock()
	defer func() {
		initializeShutdownLock.Lock()
		resetContainerWithoutLock()
		close(done)
		lifecycleChanged.Broadcast()
		initializeShutdownLock.Unlock()
	}()
	c.closeSingletons()
}

func resetContainer() {
	initializeShutdownLock.Lock()
	defer initializeShutdownLock.Unlock()
	resetContainerWithoutLock()
}

func resetContainerWithoutLock() {
	containerState = uninitialized
	runningContainer = nil
	beans = make(map[string]reflect.Type)
	beanFactories = make(map[string]func(context.Context) (interface{}, error))
	scopes = make(map[string]Scope)
	singletonInstances = make(map[string]interface{})
	userCreatedInstances = make(map[string]bool)
	beanPostprocessors = make(map[reflect.Type][]func(bean interface{}) error)
}
