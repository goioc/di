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
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/suite"
	"reflect"
	"testing"
)

type TestSuite struct {
	suite.Suite
}

func (suite *TestSuite) TearDownTest() {
	resetContainer()
}

func TestDITestSuite(t *testing.T) {
	suite.Run(t, new(TestSuite))
}

func (suite *TestSuite) TestInitializeContainerTwice() {
	err := InitializeContainer()
	assert.NoError(suite.T(), err)
	expectedError := errors.New("container is already initialized: reinitialization is not supported")
	err = InitializeContainer()
	if assert.Error(suite.T(), err) {
		assert.Equal(suite.T(), expectedError, err)
	}
}

func (suite *TestSuite) TestReinitializeContainerAfterReset() {
	err := InitializeContainer()
	assert.NoError(suite.T(), err)
	resetContainer()
	err = InitializeContainer()
	assert.NoError(suite.T(), err)
}

func (suite *TestSuite) TestGetInstanceBeforeContainerInitialization() {
	expectedError := errors.New("container is not initialized: can't lookup instances of beans yet")
	instance, err := GetInstanceSafe("")
	assert.Nil(suite.T(), instance)
	if assert.Error(suite.T(), err) {
		assert.Equal(suite.T(), expectedError, err)
	}
}

func (suite *TestSuite) TestRegisterBeanAfterContainerInitialization() {
	err := InitializeContainer()
	assert.NoError(suite.T(), err)
	expectedError := errors.New("container is already initialized: can't register new bean")
	expectedFactoryError := errors.New("container is already initialized: can't register new bean factory")
	overwritten, err := RegisterBean("", nil)
	assert.False(suite.T(), overwritten)
	if assert.Error(suite.T(), err) {
		assert.Equal(suite.T(), expectedError, err)
	}
	overwritten, err = RegisterBeanInstance("", nil)
	assert.False(suite.T(), overwritten)
	if assert.Error(suite.T(), err) {
		assert.Equal(suite.T(), expectedError, err)
	}
	overwritten, err = RegisterBeanFactory("", Singleton, nil)
	assert.False(suite.T(), overwritten)
	assert.Error(suite.T(), err)
	assert.Equal(suite.T(), expectedFactoryError, err)
}

func (suite *TestSuite) TestBeanFactoryCalledOnce() {
	var countOfCalls = 0
	overwritten, err := RegisterBeanFactory("beanId", Singleton, func() (interface{}, error) {
		countOfCalls++
		return new(string), nil
	})
	assert.False(suite.T(), overwritten)
	assert.Nil(suite.T(), err)
	err = InitializeContainer()
	assert.False(suite.T(), overwritten)
	assert.Nil(suite.T(), err)
	assert.Equal(suite.T(), 1, countOfCalls)
	instance, err := GetInstanceSafe("beanId")
	assert.Nil(suite.T(), err)
	assert.NotNil(suite.T(), instance)
	assert.Equal(suite.T(), 1, countOfCalls)
}

func (suite *TestSuite) TestRegisterBeanPostprocessorAfterContainerInitialization() {
	err := InitializeContainer()
	assert.NoError(suite.T(), err)
	expectedError := errors.New("container is already initialized: can't register bean postprocessor")
	err = RegisterBeanPostprocessor(reflect.TypeOf((*string)(nil)), nil)
	if assert.Error(suite.T(), err) {
		assert.Equal(suite.T(), expectedError, err)
	}
}

func (suite *TestSuite) TestRegisterNonReferenceBean() {
	expectedError := errors.New("bean type must be a pointer")
	overwritten, err := RegisterBean("", reflect.TypeOf(""))
	assert.False(suite.T(), overwritten)
	if assert.Error(suite.T(), err) {
		assert.Equal(suite.T(), expectedError, err)
	}
}

func (suite *TestSuite) TestRegisterNonReferenceBeanInstance() {
	expectedError := errors.New("bean instance must be a pointer")
	overwritten, err := RegisterBeanInstance("", "")
	assert.False(suite.T(), overwritten)
	if assert.Error(suite.T(), err) {
		assert.Equal(suite.T(), expectedError, err)
	}
}

func (suite *TestSuite) TestRegisterNonReferenceSingletonBeanFactory() {
	overwritten, err := RegisterBeanFactory("", Singleton, func() (interface{}, error) {
		return "", nil
	})
	assert.False(suite.T(), overwritten)
	assert.NoError(suite.T(), err)
	expectedError := errors.New("bean factory must return pointer")
	err = InitializeContainer()
	if assert.Error(suite.T(), err) {
		assert.Equal(suite.T(), expectedError, err)
	}
}

func (suite *TestSuite) TestRegisterNonReferencePrototypeBeanFactory() {
	overwritten, err := RegisterBeanFactory("", Prototype, func() (interface{}, error) {
		return "", nil
	})
	assert.False(suite.T(), overwritten)
	assert.NoError(suite.T(), err)
	expectedError := errors.New("bean factory must return pointer")
	err = InitializeContainer()
	assert.NoError(suite.T(), err)
	instance, err := GetInstanceSafe("")
	assert.Nil(suite.T(), instance)
	if assert.Error(suite.T(), err) {
		assert.Equal(suite.T(), expectedError, err)
	}
}

func (suite *TestSuite) TestRegisterSingletonBeanUnsupportedScope() {
	type SingletonBean struct {
		Scope Scope `di.scope:"invalid"`
	}
	expectedError := errors.New("unsupported scope: invalid")
	overwritten, err := RegisterBean("", reflect.TypeOf((*SingletonBean)(nil)))
	assert.False(suite.T(), overwritten)
	if assert.Error(suite.T(), err) {
		assert.Equal(suite.T(), expectedError, err)
	}
}

func (suite *TestSuite) TestRegisterSingletonBeanNonReferenceDependency() {
	type SingletonBean struct {
		SomeOtherBean string `di.inject:"someOtherBean"`
	}
	expectedError := errors.New("unsupported dependency type: all injections must be done by reference")
	overwritten, err := RegisterBean("", reflect.TypeOf((*SingletonBean)(nil)))
	assert.False(suite.T(), overwritten)
	if assert.Error(suite.T(), err) {
		assert.Equal(suite.T(), expectedError, err)
	}
}

func (suite *TestSuite) TestRegisterSingletonBeanWrongOptionalValue() {
	type SingletonBean struct {
		SomeOtherBean *string `di.inject:"someOtherBean" di.optional:"fls"`
	}
	overwritten, err := RegisterBean("singletonBean", reflect.TypeOf((*SingletonBean)(nil)))
	assert.False(suite.T(), overwritten)
	assert.NoError(suite.T(), err)
	expectedError := errors.New("invalid di.optional value: fls")
	err = InitializeContainer()
	if assert.Error(suite.T(), err) {
		assert.Equal(suite.T(), expectedError, err)
	}
}

func (suite *TestSuite) TestRegisterSingletonBeanMissingImplicitlyRequiredDependency() {
	type SingletonBean struct {
		SomeOtherBean *string `di.inject:"someOtherBean"`
	}
	overwritten, err := RegisterBean("singletonBean", reflect.TypeOf((*SingletonBean)(nil)))
	assert.False(suite.T(), overwritten)
	assert.NoError(suite.T(), err)
	expectedError := errors.New("no dependency found")
	err = InitializeContainer()
	if assert.Error(suite.T(), err) {
		assert.Equal(suite.T(), expectedError, err)
	}
}

func (suite *TestSuite) TestRegisterSingletonBeanMissingExplicitlyRequiredDependency() {
	type SingletonBean struct {
		SomeOtherBean *string `di.inject:"someOtherBean" di.optional:"false"`
	}
	overwritten, err := RegisterBean("singletonBean", reflect.TypeOf((*SingletonBean)(nil)))
	assert.False(suite.T(), overwritten)
	assert.NoError(suite.T(), err)
	expectedError := errors.New("no dependency found")
	err = InitializeContainer()
	if assert.Error(suite.T(), err) {
		assert.Equal(suite.T(), expectedError, err)
	}
}

func (suite *TestSuite) TestRegisterSingletonBeanMissingOptionalDependency() {
	type SingletonBean struct {
		SomeOtherBean *string `di.inject:"someOtherBean" di.optional:"true"`
	}
	overwritten, err := RegisterBean("singletonBean", reflect.TypeOf((*SingletonBean)(nil)))
	assert.False(suite.T(), overwritten)
	assert.NoError(suite.T(), err)
	err = InitializeContainer()
	assert.NoError(suite.T(), err)
}

func (suite *TestSuite) TestRegisterBeanInstanceWithOverwriting() {
	overwritten, err := RegisterBeanInstance("bean", new(string))
	assert.False(suite.T(), overwritten)
	assert.NoError(suite.T(), err)
	overwritten, err = RegisterBeanInstance("bean", new(string))
	assert.True(suite.T(), overwritten)
	assert.NoError(suite.T(), err)
	err = InitializeContainer()
	assert.NoError(suite.T(), err)
}

func (suite *TestSuite) TestRegisterBeanWithOverwriting() {
	type Bean1 struct {
	}
	type Bean2 struct {
	}
	overwritten, err := RegisterBean("bean", reflect.TypeOf((*Bean1)(nil)))
	assert.False(suite.T(), overwritten)
	assert.NoError(suite.T(), err)
	overwritten, err = RegisterBean("bean", reflect.TypeOf((*Bean2)(nil)))
	assert.True(suite.T(), overwritten)
	assert.NoError(suite.T(), err)
	err = InitializeContainer()
	assert.NoError(suite.T(), err)
	bean2 := GetInstance("bean").(*Bean2)
	assert.NotNil(suite.T(), bean2)
}

func (suite *TestSuite) TestRegisterBeanWithOverwritingFromSingletonToPrototypeScope() {
	type SingletonBean struct {
		Scope Scope `di.scope:"singleton"`
	}
	type PrototypeBean struct {
		Scope Scope `di.scope:"prototype"`
	}
	overwritten, err := RegisterBean("bean", reflect.TypeOf((*SingletonBean)(nil)))
	assert.False(suite.T(), overwritten)
	assert.NoError(suite.T(), err)
	overwritten, err = RegisterBean("bean", reflect.TypeOf((*PrototypeBean)(nil)))
	assert.True(suite.T(), overwritten)
	assert.NoError(suite.T(), err)
	err = InitializeContainer()
	assert.NoError(suite.T(), err)
	instance1 := GetInstance("bean").(*PrototypeBean)
	instance2 := GetInstance("bean").(*PrototypeBean)
	assert.False(suite.T(), instance1 == instance2)
}

func (suite *TestSuite) TestRegisterSingletonBeanImplicitScope() {
	type SingletonBean struct {
		someField string
	}
	overwritten, err := RegisterBean("singletonBean", reflect.TypeOf((*SingletonBean)(nil)))
	assert.False(suite.T(), overwritten)
	assert.NoError(suite.T(), err)
	err = InitializeContainer()
	assert.NoError(suite.T(), err)
	instance1 := GetInstance("singletonBean").(*SingletonBean)
	instance2 := GetInstance("singletonBean").(*SingletonBean)
	assert.True(suite.T(), instance1 == instance2)
}

func (suite *TestSuite) TestRegisterSingletonBeanExplicitScope() {
	type SingletonBean struct {
		Scope Scope `di.scope:"singleton"`
	}
	overwritten, err := RegisterBean("singletonBean", reflect.TypeOf((*SingletonBean)(nil)))
	assert.False(suite.T(), overwritten)
	assert.NoError(suite.T(), err)
	err = InitializeContainer()
	assert.NoError(suite.T(), err)
	instance1 := GetInstance("singletonBean").(*SingletonBean)
	instance2 := GetInstance("singletonBean").(*SingletonBean)
	assert.True(suite.T(), instance1 == instance2)
}

func (suite *TestSuite) TestRegisterSingletonBeanInstance() {
	overwritten, err := RegisterBeanInstance("singletonBean", new(string))
	assert.False(suite.T(), overwritten)
	assert.NoError(suite.T(), err)
	err = InitializeContainer()
	assert.NoError(suite.T(), err)
	instance1 := GetInstance("singletonBean").(*string)
	instance2 := GetInstance("singletonBean").(*string)
	assert.True(suite.T(), instance1 == instance2)
}

func (suite *TestSuite) TestRegisterSingletonBeanFactoryWithError() {
	overwritten, err := RegisterBeanFactory("singletonBean", Singleton, func() (interface{}, error) {
		return nil, errors.New("error in the bean factory")
	})
	assert.False(suite.T(), overwritten)
	assert.NoError(suite.T(), err)
	expectedError := errors.New("error in the bean factory")
	err = InitializeContainer()
	if assert.Error(suite.T(), err) {
		assert.Equal(suite.T(), expectedError, err)
	}
}

func (suite *TestSuite) TestRegisterSingletonBeanFactory() {
	overwritten, err := RegisterBeanFactory("singletonBean", Singleton, func() (interface{}, error) {
		return new(string), nil
	})
	assert.False(suite.T(), overwritten)
	assert.NoError(suite.T(), err)
	err = InitializeContainer()
	assert.NoError(suite.T(), err)
	instance1 := GetInstance("singletonBean").(*string)
	instance2 := GetInstance("singletonBean").(*string)
	assert.True(suite.T(), instance1 == instance2)
}

func (suite *TestSuite) TestRegisterPrototypeBean() {
	type PrototypeBean struct {
		Scope Scope `di.scope:"prototype"`
	}
	overwritten, err := RegisterBean("prototypeBean", reflect.TypeOf((*PrototypeBean)(nil)))
	assert.False(suite.T(), overwritten)
	assert.NoError(suite.T(), err)
	err = InitializeContainer()
	assert.NoError(suite.T(), err)
	instance1 := GetInstance("prototypeBean").(*PrototypeBean)
	instance2 := GetInstance("prototypeBean").(*PrototypeBean)
	assert.True(suite.T(), instance1 != instance2)
}

func (suite *TestSuite) TestRegisterPrototypeBeanFactoryWithError() {
	overwritten, err := RegisterBeanFactory("prototypeBean", Prototype, func() (interface{}, error) {
		return nil, errors.New("error in the bean factory")
	})
	assert.False(suite.T(), overwritten)
	assert.NoError(suite.T(), err)
	err = InitializeContainer()
	assert.NoError(suite.T(), err)
	expectedError := errors.New("error in the bean factory")
	instance, err := GetInstanceSafe("prototypeBean")
	assert.Nil(suite.T(), instance)
	if assert.Error(suite.T(), err) {
		assert.Equal(suite.T(), expectedError, err)
	}
}

func (suite *TestSuite) TestRegisterPrototypeBeanFactory() {
	overwritten, err := RegisterBeanFactory("prototypeBean", Prototype, func() (interface{}, error) {
		return new(string), nil
	})
	assert.False(suite.T(), overwritten)
	assert.NoError(suite.T(), err)
	err = InitializeContainer()
	assert.NoError(suite.T(), err)
	instance1 := GetInstance("prototypeBean").(*string)
	instance2 := GetInstance("prototypeBean").(*string)
	assert.True(suite.T(), instance1 != instance2)
}

type beanWithInjectedBeanFactory struct {
	BeanFactoryDependency *string `di.inject:"beanFactory"`
}

func (suite *TestSuite) TestInjectingBeanFactory() {
	overwritten, err := RegisterBeanFactory("beanFactory", Singleton, func() (interface{}, error) {
		s := "test"
		return &s, nil
	})
	assert.False(suite.T(), overwritten)
	assert.NoError(suite.T(), err)
	overwritten, err = RegisterBean("beanWithInjectedBeanFactory", reflect.TypeOf((*beanWithInjectedBeanFactory)(nil)))
	assert.False(suite.T(), overwritten)
	assert.NoError(suite.T(), err)
	err = InitializeContainer()
	assert.NoError(suite.T(), err)
	instance, err := GetInstanceSafe("beanWithInjectedBeanFactory")
	assert.NotNil(suite.T(), instance)
	assert.NoError(suite.T(), err)
	beanWithInjectedBeanFactory := instance.(*beanWithInjectedBeanFactory)
	assert.Equal(suite.T(), "test", *beanWithInjectedBeanFactory.BeanFactoryDependency)
}

func (suite *TestSuite) TestInjectingBeanFactoryWithOverwriting() {
	overwritten, err := RegisterBeanFactory("beanFactory", Singleton, func() (interface{}, error) {
		s := "test"
		return &s, nil
	})
	assert.False(suite.T(), overwritten)
	assert.NoError(suite.T(), err)
	overwritten, err = RegisterBeanFactory("beanFactory", Singleton, func() (interface{}, error) {
		s := "test_overwritten"
		return &s, nil
	})
	assert.True(suite.T(), overwritten)
	assert.NoError(suite.T(), err)
	err = InitializeContainer()
	assert.NoError(suite.T(), err)
	instance, err := GetInstanceSafe("beanFactory")
	assert.NotNil(suite.T(), instance)
	assert.NoError(suite.T(), err)
	assert.Equal(suite.T(), "test_overwritten", *instance.(*string))
}

func (suite *TestSuite) TestInjectingBeanFactoryWithOverwritingFromSingletonToPrototypeScope() {
	overwritten, err := RegisterBeanFactory("beanFactory", Singleton, func() (interface{}, error) {
		s := "test"
		return &s, nil
	})
	assert.False(suite.T(), overwritten)
	assert.NoError(suite.T(), err)
	overwritten, err = RegisterBeanFactory("beanFactory", Prototype, func() (interface{}, error) {
		s := "test_overwritten"
		return &s, nil
	})
	assert.True(suite.T(), overwritten)
	assert.NoError(suite.T(), err)
	err = InitializeContainer()
	assert.NoError(suite.T(), err)
	instance, err := GetInstanceSafe("beanFactory")
	assert.NotNil(suite.T(), instance)
	assert.NoError(suite.T(), err)
	assert.Equal(suite.T(), "test_overwritten", *instance.(*string))
	instance2, err := GetInstanceSafe("beanFactory")
	assert.False(suite.T(), instance == instance2)
}

func (suite *TestSuite) TestBeanFunction() {
	overwritten, err := RegisterBeanFactory("beanFunction", Singleton, func() (interface{}, error) {
		f := func(x int) int {
			return x + 42
		}
		return &f, nil
	})
	assert.False(suite.T(), overwritten)
	assert.NoError(suite.T(), err)
	err = InitializeContainer()
	assert.NoError(suite.T(), err)
	instance, err := GetInstanceSafe("beanFunction")
	assert.NotNil(suite.T(), instance)
	assert.NoError(suite.T(), err)
	beanFunction := *instance.(*func(int) int)
	assert.Equal(suite.T(), 43, beanFunction(1))
}

type failingSingletonBean struct {
}

func (fsb *failingSingletonBean) PostConstruct() error {
	return errors.New("error message")
}

func (suite *TestSuite) TestSingletonPostConstructReturnsError() {
	overwritten, err := RegisterBean("failingSingletonBean", reflect.TypeOf((*failingSingletonBean)(nil)))
	assert.False(suite.T(), overwritten)
	assert.NoError(suite.T(), err)
	expectedError := errors.New("error message")
	err = InitializeContainer()
	if assert.Error(suite.T(), err) {
		assert.Equal(suite.T(), expectedError, err)
	}
}

type failingPrototypeBean struct {
	Scope Scope `di.scope:"prototype"`
}

func (fpb *failingPrototypeBean) PostConstruct() error {
	return errors.New("error message")
}

func (suite *TestSuite) TestPrototypePostConstructReturnsError() {
	overwritten, err := RegisterBean("failingPrototypeBean", reflect.TypeOf((*failingPrototypeBean)(nil)))
	assert.False(suite.T(), overwritten)
	assert.NoError(suite.T(), err)
	err = InitializeContainer()
	assert.NoError(suite.T(), err)
	expectedError := errors.New("error message")
	beanInstance, err := GetInstanceSafe("failingPrototypeBean")
	assert.Nil(suite.T(), beanInstance)
	if assert.Error(suite.T(), err) {
		assert.Equal(suite.T(), expectedError, err)
	}
}

type postConstructBean1 struct {
	Value string
}

func (pcb *postConstructBean1) PostConstruct() error {
	pcb.Value = "some content"
	return nil
}

type postConstructBean2 struct {
	Scope              Scope `di.scope:"prototype"`
	PostConstructBean1 *postConstructBean1
}

func (pcb *postConstructBean2) PostConstruct() error {
	instance, err := GetInstanceSafe("postConstructBean1")
	if err != nil {
		return err
	}
	pcb.PostConstructBean1 = instance.(*postConstructBean1)
	return nil
}

type postConstructBean3 struct {
	PostConstructBean2 *postConstructBean2
}

func (pcb *postConstructBean3) PostConstruct() error {
	instance, err := GetInstanceSafe("postConstructBean2")
	if err != nil {
		return err
	}
	pcb.PostConstructBean2 = instance.(*postConstructBean2)
	return nil
}

func (suite *TestSuite) TestPostConstruct() {
	overwritten, err := RegisterBean("postConstructBean1", reflect.TypeOf((*postConstructBean1)(nil)))
	assert.False(suite.T(), overwritten)
	assert.NoError(suite.T(), err)
	overwritten, err = RegisterBean("postConstructBean2", reflect.TypeOf((*postConstructBean2)(nil)))
	assert.False(suite.T(), overwritten)
	assert.NoError(suite.T(), err)
	overwritten, err = RegisterBean("postConstructBean3", reflect.TypeOf((*postConstructBean3)(nil)))
	assert.False(suite.T(), overwritten)
	assert.NoError(suite.T(), err)
	err = InitializeContainer()
	assert.NoError(suite.T(), err)
	assert.Equal(suite.T(), "some content", GetInstance("postConstructBean3").(*postConstructBean3).PostConstructBean2.PostConstructBean1.Value)
}

func (suite *TestSuite) TestBeanPostprocessorReturnsError() {
	overwritten, err := RegisterBeanInstance("postprocessedBean", new(string))
	assert.False(suite.T(), overwritten)
	assert.NoError(suite.T(), err)
	expectedError := errors.New("some error")
	err = RegisterBeanPostprocessor(reflect.TypeOf((*string)(nil)), func(instance interface{}) error {
		return expectedError
	})
	err = InitializeContainer()
	if assert.Error(suite.T(), err) {
		assert.Equal(suite.T(), expectedError, err)
	}
}

type postprocessedBean struct {
	a string
	b string
}

func (suite *TestSuite) TestBeanPostprocessors() {
	overwritten, err := RegisterBean("postprocessedBean", reflect.TypeOf((*postprocessedBean)(nil)))
	assert.False(suite.T(), overwritten)
	assert.NoError(suite.T(), err)
	err = RegisterBeanPostprocessor(reflect.TypeOf((*postprocessedBean)(nil)), func(instance interface{}) error {
		instance.(*postprocessedBean).a = "Hello, "
		return nil
	})
	err = RegisterBeanPostprocessor(reflect.TypeOf((*postprocessedBean)(nil)), func(instance interface{}) error {
		instance.(*postprocessedBean).b = "world!"
		return nil
	})
	err = InitializeContainer()
	assert.NoError(suite.T(), err)
	instance, err := GetInstanceSafe("postprocessedBean")
	assert.NoError(suite.T(), err)
	assert.NotNil(suite.T(), instance)
	postprocessedBean := instance.(*postprocessedBean)
	assert.Equal(suite.T(), "Hello, world!", postprocessedBean.a+postprocessedBean.b)
}

type circularBean struct {
	Scope        Scope         `di.scope:"prototype"`
	CircularBean *circularBean `di.inject:"circularBean"`
}

func (suite *TestSuite) TestDirectCircularDependency() {
	overwritten, err := RegisterBean("circularBean", reflect.TypeOf((*circularBean)(nil)))
	assert.False(suite.T(), overwritten)
	assert.NoError(suite.T(), err)
	err = InitializeContainer()
	assert.NoError(suite.T(), err)
	expectedError := errors.New("circular dependency detected for bean: circularBean")
	instance, err := GetInstanceSafe("circularBean")
	assert.Nil(suite.T(), instance)
	if assert.Error(suite.T(), err) {
		assert.Equal(suite.T(), expectedError, err)
	}
}

func (suite *TestSuite) TestRequestBeanInjection() {
	type RequestBean struct {
		Scope Scope `di.scope:"request"`
	}
	type SingletonBean struct {
		RequestBean *string `di.inject:"requestBean"`
	}
	overwritten, err := RegisterBean("singletonBean", reflect.TypeOf((*SingletonBean)(nil)))
	assert.False(suite.T(), overwritten)
	assert.NoError(suite.T(), err)
	overwritten, err = RegisterBean("requestBean", reflect.TypeOf((*RequestBean)(nil)))
	assert.False(suite.T(), overwritten)
	assert.NoError(suite.T(), err)
	expectedError := errors.New("request-scoped beans can't be injected: they can only be retrieved from the web-context")
	err = InitializeContainer()
	if assert.Error(suite.T(), err) {
		assert.Equal(suite.T(), expectedError, err)
	}
}

func (suite *TestSuite) TestRequestBeanRetrieval() {
	type RequestBean struct {
		Scope Scope `di.scope:"request"`
	}
	overwritten, err := RegisterBean("requestBean", reflect.TypeOf((*RequestBean)(nil)))
	assert.False(suite.T(), overwritten)
	assert.NoError(suite.T(), err)
	err = InitializeContainer()
	assert.NoError(suite.T(), err)
	expectedError := errors.New("request-scoped beans can't be retrieved directly from the container: they can only be retrieved from the web-context")
	instance, err := GetInstanceSafe("requestBean")
	assert.Nil(suite.T(), instance)
	if assert.Error(suite.T(), err) {
		assert.Equal(suite.T(), expectedError, err)
	}
	assert.Panics(suite.T(), func() {
		GetInstance("requestBean")
	})
}

func (suite *TestSuite) TestFailRequestBeanRetrieval() {
	overwritten, err := RegisterBeanFactory("requestBean", Request, func() (interface{}, error) {
		return nil, errors.New("Cannot initialize request bean")
	})
	assert.False(suite.T(), overwritten)
	assert.NoError(suite.T(), err)
	err = InitializeContainer()
	assert.NoError(suite.T(), err)
	assert.Panics(suite.T(), func() {
		getRequestBeanInstance("requestBean")
	})
}

//func (suite *TestSuite) TestFailSingletonBeanRetrieval() {
//	type SingletonBean struct {
//	}
//	expectedError := errors.New("Cannot initialize request bean")
//	overwritten, err := RegisterBean("singletonBean", reflect.TypeOf((*SingletonBean)(nil)))
//	assert.False(suite.T(), overwritten)
//	assert.NoError(suite.T(), err)
//	testHookCreateInstanceOriginal:=testHookCreateInstance
//	defer func() {
//		testHookCreateInstance = testHookCreateInstanceOriginal
//	}()
//
//	testHookCreateInstance = func(beanID string) (interface{}, error) {
//		return nil,expectedError
//	}
//	err = InitializeContainer()
//	assert.Error(suite.T(), err)
//	assert.Equal(suite.T(), expectedError, err)
//}

func (suite *TestSuite) TestGetBeanTypes() {
	type SomeBean struct {
		Scope Scope `di.scope:"prototype"`
	}
	overwritten, err := RegisterBean("bean", reflect.TypeOf((*SomeBean)(nil)))
	assert.False(suite.T(), overwritten)
	assert.NoError(suite.T(), err)
	overwritten, err = RegisterBeanInstance("beanInstance", new(string))
	assert.False(suite.T(), overwritten)
	assert.NoError(suite.T(), err)
	beansTypes := GetBeanTypes()
	assert.Len(suite.T(), beansTypes, 2)
	assert.Contains(suite.T(), beansTypes, "bean")
	bean := beansTypes["bean"]
	assert.Equal(suite.T(), reflect.TypeOf((*SomeBean)(nil)), bean)
	assert.Contains(suite.T(), beansTypes, "beanInstance")
	beanInstance := beansTypes["beanInstance"]
	assert.Equal(suite.T(), reflect.TypeOf((*string)(nil)), beanInstance)
	beansTypes["newBean"] = nil
	assert.Len(suite.T(), GetBeanTypes(), 2)
}

func (suite *TestSuite) TestGetBeanScopes() {
	type SomeBean struct {
		Scope Scope `di.scope:"prototype"`
	}
	overwritten, err := RegisterBean("bean", reflect.TypeOf((*SomeBean)(nil)))
	assert.False(suite.T(), overwritten)
	assert.NoError(suite.T(), err)
	overwritten, err = RegisterBeanInstance("beanInstance", new(string))
	assert.False(suite.T(), overwritten)
	assert.NoError(suite.T(), err)
	overwritten, err = RegisterBeanFactory("beanFactory", Request, func() (interface{}, error) {
		return new(string), nil
	})
	assert.False(suite.T(), overwritten)
	assert.NoError(suite.T(), err)
	beansScopes := GetBeanScopes()
	assert.Len(suite.T(), beansScopes, 3)
	assert.Contains(suite.T(), beansScopes, "bean")
	bean := beansScopes["bean"]
	assert.Equal(suite.T(), Prototype, bean)
	assert.Contains(suite.T(), beansScopes, "beanInstance")
	beanInstance := beansScopes["beanInstance"]
	assert.Equal(suite.T(), Singleton, beanInstance)
	assert.Contains(suite.T(), beansScopes, "beanInstance")
	beanFactory := beansScopes["beanFactory"]
	assert.Equal(suite.T(), Request, beanFactory)
	beansScopes["newBean"] = Singleton
	assert.Len(suite.T(), GetBeanScopes(), 3)
}
