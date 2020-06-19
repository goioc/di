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
var containerInitialized int32 = 0
var beans = make(map[string]reflect.Type)
var beanFactories = make(map[string]func() (interface{}, error))
var scopes = make(map[string]Scope)
var singletonInstances = make(map[string]interface{})
var userCreatedInstances = make(map[string]bool)

type Scope string

const (
	Singleton Scope = "singleton"
	Prototype Scope = "prototype"
)

type InitializingBean interface {
	PostConstruct() error
}

func init() {
	logrus.SetFormatter(&logrus.TextFormatter{})
}

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

func RegisterBeanFactory(beanID string, beanScope Scope, beanFactory func() (interface{}, error)) (overwritten bool, err error) {
	initializeLock.Lock()
	defer initializeLock.Unlock()
	if atomic.CompareAndSwapInt32(&containerInitialized, 1, 1) {
		return false, errors.New("container is already initialized: can't register new bean")
	}
	var existingBeanType reflect.Type
	var ok bool
	if existingBeanType, ok = beans[beanID]; ok {
		logrus.WithFields(logrus.Fields{
			"id":              beanID,
			"registered bean": existingBeanType,
		}).Warn("Bean with such ID is already registered, overwriting it")
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
	if !ok {
		return &singleton, nil
	}
	switch scope {
	case string(Singleton):
		return &singleton, nil
	case string(Prototype):
		return &prototype, nil
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
		instanceToInject, err := getInstance(beanToInject, chain)
		if err != nil {
			return err
		}
		if instanceToInject == nil {
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
		initializingMethod, ok := bean.MethodByName(initializingBean.Method(0).Name)
		if !ok {
			return errors.New("can't find method PostConstruct() in bean " + bean.String())
		}
		logrus.WithField("beanID", beanID).Trace("Initializing bean")
		errorValue := initializingMethod.Func.Call([]reflect.Value{reflect.ValueOf(instance)})[0]
		if !errorValue.IsNil() {
			return errorValue.Elem().Interface().(error)
		}
	}
	return nil
}

func GetInstance(beanID string) interface{} {
	if atomic.CompareAndSwapInt32(&containerInitialized, 0, 0) {
		panic("container is not initialized: can't lookup instances of beans yet")
	}
	instance, err := getInstance(beanID, make(map[string]bool))
	if err != nil {
		panic(err)
	}
	return instance
}

func GetInstanceSafe(beanID string) (interface{}, error) {
	if atomic.CompareAndSwapInt32(&containerInitialized, 0, 0) {
		return nil, errors.New("container is not initialized: can't lookup instances of beans yet")
	}
	return getInstance(beanID, make(map[string]bool))
}

func getInstance(beanID string, chain map[string]bool) (interface{}, error) {
	switch scopes[beanID] {
	case Prototype:
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
	default:
		return singletonInstances[beanID], nil
	}
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
}
