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
	"errors"
	"github.com/sirupsen/logrus"
	"reflect"
	"strconv"
	"sync"
	"sync/atomic"
	"unsafe"
)

var initializeLock sync.Mutex
var createInstanceLock sync.Mutex
var containerInitialized int32
var beans = make(map[string]reflect.Type)
var beanFactories = make(map[string]func() (interface{}, error))
var scopes = make(map[string]Scope)
var singletonInstances = make(map[string]interface{})
var userCreatedInstances = make(map[string]bool)
var beanPostprocessors = make(map[reflect.Type][]func(bean interface{}) error)

// Scope is a enum for bean scopes supported in this IoC container.
type Scope string

const (
	// Singleton is a scope of bean that exists only in one copy in the container and is created at the init-time.
	Singleton Scope = "singleton"
	// Prototype is a scope of bean that can exist in multiple copies in the container and is created on demand.
	Prototype Scope = "prototype"
	// Request is a scope of bean whose lifecycle is bound to the web request (or more precisely - to the corresponding
	// context). If the bean implements Close() method, then this method will be called upon corresponding context's
	// cancellation.
	Request Scope = "request"
)

// InitializingBean is an interface marking beans that need to be additionally initialized after the container is ready.
type InitializingBean interface {
	// PostConstruct method will be called on a bean after the container is initialized.
	PostConstruct() error
}

func init() {
	logrus.SetFormatter(&logrus.TextFormatter{})
}

// RegisterBeanPostprocessor function registers postprocessors for beans. Postprocessor is a function that can perform
// some actions on beans after their creation by the container (and self-initialization with PostConstruct).
func RegisterBeanPostprocessor(beanType reflect.Type, postprocessor func(bean interface{}) error) error {
	initializeLock.Lock()
	defer initializeLock.Unlock()
	if atomic.CompareAndSwapInt32(&containerInitialized, 1, 1) {
		return errors.New("container is already initialized: can't register bean postprocessor")
	}
	beanPostprocessors[beanType] = append(beanPostprocessors[beanType], postprocessor)
	return nil
}

// InitializeContainer function initializes the IoC container.
func InitializeContainer() error {
	initializeLock.Lock()
	defer initializeLock.Unlock()
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
	initializeLock.Lock()
	defer initializeLock.Unlock()
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
		}).Warn("Bean with such ID is already registered, overwriting it")
	}
	scope, err := getScope(beanType)
	if err != nil {
		return false, err
	}
	beanTypeElement := beanType.Elem()
	for i := 0; i < beanTypeElement.NumField(); i++ {
		field := beanTypeElement.Field(i)
		if field.Tag.Get("di.inject") == "" {
			continue
		}
		if field.Type.Kind() != reflect.Ptr && field.Type.Kind() != reflect.Interface {
			return false, errors.New("unsupported dependency type: all injections must be done by reference")
		}
	}
	beans[beanID] = beanType
	scopes[beanID] = *scope
	return ok, nil
}

// RegisterBeanInstance function registers bean, provided the pre-created instance of this bean, the scope of such beans
// are always `Singleton`. `beanInstance` can only be a reference or an interface. Return value of `overwritten` is set
// to `true` if the bean with the same `beanID` has been registered already.
func RegisterBeanInstance(beanID string, beanInstance interface{}) (overwritten bool, err error) {
	initializeLock.Lock()
	defer initializeLock.Unlock()
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
		}).Warn("Bean with such ID is already registered, overwriting it")
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
func RegisterBeanFactory(beanID string, beanScope Scope, beanFactory func() (interface{}, error)) (overwritten bool, err error) {
	initializeLock.Lock()
	defer initializeLock.Unlock()
	if atomic.CompareAndSwapInt32(&containerInitialized, 1, 1) {
		return false, errors.New("container is already initialized: can't register new bean factory")
	}
	var ok bool
	if _, ok = beanFactories[beanID]; ok {
		logrus.WithFields(logrus.Fields{
			"id": beanID,
		}).Warn("Bean Factory with such ID is already registered, overwriting it")
	}
	scopes[beanID] = beanScope
	beanFactories[beanID] = beanFactory
	return ok, nil
}

func getScope(bean reflect.Type) (*Scope, error) {
	var scope string
	ok := false
	beanElement := bean.Elem()
	for i := 0; i < beanElement.NumField(); i++ {
		field := beanElement.Field(i)
		scope, ok = field.Tag.Lookup("di.scope")
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
	switch scope {
	case string(Singleton):
		return &singleton, nil
	case string(Prototype):
		return &prototype, nil
	case string(Request):
		return &request, nil
	}
	return nil, errors.New("unsupported scope: " + scope)
}

func injectSingletonDependencies() error {
	for beanID, instance := range singletonInstances {
		if _, ok := userCreatedInstances[beanID]; ok {
			continue
		}
		if _, ok := beanFactories[beanID]; ok {
			continue
		}
		err := injectDependencies(beanID, instance, make(map[string]bool))
		if err != nil {
			return err
		}
	}
	return nil
}

func injectDependencies(beanID string, instance interface{}, chain map[string]bool) error {
	logrus.WithField("beanID", beanID).Trace("Injecting dependencies")
	instanceType := beans[beanID]
	instanceElement := instanceType.Elem()
	for i := 0; i < instanceElement.NumField(); i++ {
		field := instanceElement.Field(i)
		beanToInject := field.Tag.Get("di.inject")
		if beanToInject == "" {
			continue
		}
		beanToInjectType := beans[beanToInject]
		fieldToInject := reflect.ValueOf(instance).Elem().Field(i)
		fieldToInject = reflect.NewAt(fieldToInject.Type(), unsafe.Pointer(fieldToInject.UnsafeAddr())).Elem()
		logrus.WithFields(logrus.Fields{
			"bean":                 beanID,
			"bean type":            instanceElement,
			"dependency bean":      beanToInject,
			"dependency bean type": beanToInjectType,
		}).Trace("Processing dependency")
		if scopes[beanToInject] == Request {
			return errors.New("request-scoped beans can't be injected: they can only be retrieved from the web-context")
		}
		_, beanFound := beans[beanToInject]
		_, beanFactoryFound := beanFactories[beanToInject]
		if !beanFound && !beanFactoryFound {
			optional := field.Tag.Get("di.optional")
			value, err := strconv.ParseBool(optional)
			if optional != "" && err != nil {
				return errors.New("invalid di.optional value: " + optional)
			}
			if value {
				logrus.Trace("No dependency found, injecting nil since the dependency marked as optional")
				continue
			} else {
				return errors.New("no dependency found")
			}
		}
		instanceToInject, err := getInstance(beanToInject, chain)
		if err != nil {
			return err
		}
		//todo validation code part can be moved above for fail-fast purposes
		if fieldToInject.Kind() == reflect.Ptr || fieldToInject.Kind() == reflect.Interface {
			fieldToInject.Set(reflect.ValueOf(instanceToInject))
		} else {
			return errors.New("unsupported dependency type: all injections must be done by reference")
		}
	}
	return nil
}

func createSingletonInstances() error {
	for beanID := range beans {
		if scopes[beanID] != Singleton {
			continue
		}
		if _, ok := userCreatedInstances[beanID]; ok {
			continue
		}
		instance, err := createInstance(beanID)
		if err != nil {
			return err
		}
		singletonInstances[beanID] = instance
		logrus.WithFields(logrus.Fields{
			"beanID": beanID,
			"scope":  scopes[beanID],
		}).Trace("Singleton instance created")
	}
	for beanID, beanFactory := range beanFactories {
		if scopes[beanID] != Singleton {
			continue
		}
		beanInstance, err := beanFactory()
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
		}).Trace("Singleton instance created")
	}
	return nil
}

func createInstance(beanID string) (interface{}, error) {
	createInstanceLock.Lock()
	defer createInstanceLock.Unlock()
	if beanFactory, ok := beanFactories[beanID]; ok {
		beanInstance, err := beanFactory()
		if err != nil {
			return nil, err
		}
		if reflect.TypeOf(beanInstance).Kind() != reflect.Ptr {
			return nil, errors.New("bean factory must return pointer")
		}
		return beanInstance, nil
	}
	logrus.WithField("beanID", beanID).Trace("Creating instance")
	return reflect.New(beans[beanID].Elem()).Interface(), nil
}

func initializeSingletonInstances() error {
	for beanID, instance := range singletonInstances {
		err := initializeInstance(beanID, instance)
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
		initializingMethod, ok := bean.MethodByName("PostConstruct")
		if !ok {
			return errors.New("Unexpected Behavior: Can't find method PostConstruct() in bean " + bean.String())
		}
		logrus.WithField("beanID", beanID).Trace("Initializing bean")
		errorValue := initializingMethod.Func.Call([]reflect.Value{reflect.ValueOf(instance)})[0]
		if !errorValue.IsNil() {
			return errorValue.Elem().Interface().(error)
		}
	}
	if postprocessors, ok := beanPostprocessors[bean]; ok {
		logrus.WithField("beanID", beanID).Trace("Postprocessing bean")
		for _, postprocessor := range postprocessors {
			if err := postprocessor(instance); err != nil {
				return err
			}
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
	if atomic.CompareAndSwapInt32(&containerInitialized, 0, 0) {
		return nil, errors.New("container is not initialized: can't lookup instances of beans yet")
	}
	if scopes[beanID] == Request {
		return nil, errors.New("request-scoped beans can't be retrieved directly from the container: they can only be retrieved from the web-context")
	}
	return getInstance(beanID, make(map[string]bool))
}

func getRequestBeanInstance(beanID string) interface{} {
	if atomic.CompareAndSwapInt32(&containerInitialized, 0, 0) {
		panic("container is not initialized: can't lookup instances of beans yet")
	}
	beanInstance, err := getInstance(beanID, make(map[string]bool))
	if err != nil {
		panic(err)
	}
	return beanInstance
}

func getInstance(beanID string, chain map[string]bool) (interface{}, error) {
	if scopes[beanID] == Singleton {
		return singletonInstances[beanID], nil
	}
	if _, ok := chain[beanID]; ok {
		return nil, errors.New("circular dependency detected for bean: " + beanID)
	}
	chain[beanID] = true
	instance, err := createInstance(beanID)
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
	return instance, nil
}

// GetBeanTypes returns a map (copy) of beans registered in the Container, omitting bean factories, because their real
// return type is unknown.
func GetBeanTypes() map[string]reflect.Type {
	initializeLock.Lock()
	defer initializeLock.Unlock()
	beanTypes := make(map[string]reflect.Type)
	for k, v := range beans {
		beanTypes[k] = v
	}
	return beanTypes
}

// GetBeanScopes returns a map (copy) of bean scopes registered in the Container.
func GetBeanScopes() map[string]Scope {
	initializeLock.Lock()
	defer initializeLock.Unlock()
	beanScopes := make(map[string]Scope)
	for k, v := range scopes {
		beanScopes[k] = v
	}
	return beanScopes
}

func resetContainer() {
	initializeLock.Lock()
	defer initializeLock.Unlock()
	containerInitialized = 0
	beans = make(map[string]reflect.Type)
	beanFactories = make(map[string]func() (interface{}, error))
	scopes = make(map[string]Scope)
	singletonInstances = make(map[string]interface{})
	userCreatedInstances = make(map[string]bool)
	beanPostprocessors = make(map[reflect.Type][]func(bean interface{}) error)
}
