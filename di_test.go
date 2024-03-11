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
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/suite"
)

type TestSuite struct {
	suite.Suite
}

func (*TestSuite) TearDownTest() {
	resetContainer()
	closedSingletons = nil
	singletonBeansWithErrorOnClose = nil
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
}

func (suite *TestSuite) TestBeanIsNotRegistered() {
	err := InitializeContainer()
	assert.NoError(suite.T(), err)
	expectedError := errors.New("bean is not registered: someBean")
	instance, err := GetInstanceSafe("someBean")
	assert.Nil(suite.T(), instance)
	if assert.Error(suite.T(), err) {
		assert.Equal(suite.T(), expectedError, err)
	}
}

func (suite *TestSuite) TestBeanFactoryCalledOnce() {
	var countOfCalls = 0
	overwritten, err := RegisterBeanFactory("beanId", Singleton, func(context.Context) (interface{}, error) {
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
	overwritten, err := RegisterBeanFactory("", Singleton, func(context.Context) (interface{}, error) {
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
	overwritten, err := RegisterBeanFactory("", Prototype, func(context.Context) (interface{}, error) {
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
	expectedError := errors.New(unsupportedDependencyType)
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
	overwritten, err := RegisterBeanFactory("singletonBean", Singleton, func(context.Context) (interface{}, error) {
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
	overwritten, err := RegisterBeanFactory("singletonBean", Singleton, func(context.Context) (interface{}, error) {
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

func (suite *TestSuite) TestRegisterBeanFactoryAfterContainerInitialization() {
	err := InitializeContainer()
	assert.NoError(suite.T(), err)
	expectedFactoryError := errors.New("container is already initialized: can't register new bean factory")
	overwritten, err := RegisterBeanFactory("", Singleton, nil)
	assert.False(suite.T(), overwritten)
	assert.Error(suite.T(), err)
	assert.Equal(suite.T(), expectedFactoryError, err)
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
	overwritten, err := RegisterBeanFactory("prototypeBean", Prototype, func(context.Context) (interface{}, error) {
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
	overwritten, err := RegisterBeanFactory("prototypeBean", Prototype, func(context.Context) (interface{}, error) {
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

func (suite *TestSuite) TestInjectSlice() {
	strings := []string{"string0", "string1"}
	overwritten, err := RegisterBeanInstance("strings", &strings)
	assert.False(suite.T(), overwritten)
	assert.NoError(suite.T(), err)
	type BeanWithSlice struct {
		strings *[]string `di.inject:"strings"`
	}
	overwritten, err = RegisterBean("beanWithSlice", reflect.TypeOf((*BeanWithSlice)(nil)))
	assert.False(suite.T(), overwritten)
	assert.NoError(suite.T(), err)
	err = InitializeContainer()
	assert.NoError(suite.T(), err)
	beanWithSlice, err := GetInstanceSafe("beanWithSlice")
	assert.NotNil(suite.T(), beanWithSlice)
	assert.NoError(suite.T(), err)
	stringsOfBean := beanWithSlice.(*BeanWithSlice).strings
	assert.NotNil(suite.T(), stringsOfBean)
	assert.Len(suite.T(), *stringsOfBean, 2)
	assert.Equal(suite.T(), "string0", (*stringsOfBean)[0])
	assert.Equal(suite.T(), "string1", (*stringsOfBean)[1])
}

func (suite *TestSuite) TestInjectMap() {
	dict := map[string]string{"key0": "value0", "key1": "value1"}
	overwritten, err := RegisterBeanInstance("dict", &dict)
	assert.False(suite.T(), overwritten)
	assert.NoError(suite.T(), err)
	type BeanWithMap struct {
		dict *map[string]string `di.inject:"dict"`
	}
	overwritten, err = RegisterBean("beanWithMap", reflect.TypeOf((*BeanWithMap)(nil)))
	assert.False(suite.T(), overwritten)
	assert.NoError(suite.T(), err)
	err = InitializeContainer()
	assert.NoError(suite.T(), err)
	beanWithMap, err := GetInstanceSafe("beanWithMap")
	assert.NotNil(suite.T(), beanWithMap)
	assert.NoError(suite.T(), err)
	dictOfBean := beanWithMap.(*BeanWithMap).dict
	assert.NotNil(suite.T(), dictOfBean)
	assert.Len(suite.T(), *dictOfBean, 2)
	assert.Equal(suite.T(), "value0", (*dictOfBean)["key0"])
	assert.Equal(suite.T(), "value1", (*dictOfBean)["key1"])
}

func (suite *TestSuite) TestInjectBeanFactory() {
	overwritten, err := RegisterBeanFactory("beanFactory", Singleton, func(context.Context) (interface{}, error) {
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

func (suite *TestSuite) TestRegisterBeanFactoryWithOverwritingFromBeanToBeanFactory() {
	type SingletonBean struct {
	}
	overwritten, err := RegisterBean("bean", reflect.TypeOf((*SingletonBean)(nil)))
	assert.False(suite.T(), overwritten)
	assert.NoError(suite.T(), err)
	overwritten, err = RegisterBeanFactory("bean", Singleton, func(context.Context) (interface{}, error) {
		s := "test_overwritten"
		return &s, nil
	})
	assert.True(suite.T(), overwritten)
	assert.NoError(suite.T(), err)
	err = InitializeContainer()
	assert.NoError(suite.T(), err)
	instance, err := GetInstanceSafe("bean")
	assert.NotNil(suite.T(), instance)
	assert.NoError(suite.T(), err)
	assert.Equal(suite.T(), "test_overwritten", *instance.(*string))
}

func (suite *TestSuite) TestRegisterBeanFactoryWithOverwritingFromPrototypeToSingletonScope() {
	type PrototypeBean struct {
		Scope Scope `di.scope:"prototype"`
	}
	overwritten, err := RegisterBean("bean", reflect.TypeOf((*PrototypeBean)(nil)))
	assert.False(suite.T(), overwritten)
	assert.NoError(suite.T(), err)
	overwritten, err = RegisterBeanFactory("bean", Singleton, func(context.Context) (interface{}, error) {
		s := "test_overwritten"
		return &s, nil
	})
	assert.True(suite.T(), overwritten)
	assert.NoError(suite.T(), err)
	err = InitializeContainer()
	assert.NoError(suite.T(), err)
	instance, err := GetInstanceSafe("bean")
	assert.NotNil(suite.T(), instance)
	assert.NoError(suite.T(), err)
	assert.Equal(suite.T(), "test_overwritten", *instance.(*string))
	instance2, err := GetInstanceSafe("bean")
	assert.NoError(suite.T(), err)
	assert.NotNil(suite.T(), instance2)
	assert.True(suite.T(), instance == instance2)
}

func (suite *TestSuite) TestBeanFunction() {
	overwritten, err := RegisterBeanFactory("beanFunction", Singleton, func(context.Context) (interface{}, error) {
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

func (*failingSingletonBean) PostConstruct() error {
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

func (*failingPrototypeBean) PostConstruct() error {
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
	assert.Nil(suite.T(), err)
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
	assert.Nil(suite.T(), err)
	err = RegisterBeanPostprocessor(reflect.TypeOf((*postprocessedBean)(nil)), func(instance interface{}) error {
		instance.(*postprocessedBean).b = "world!"
		return nil
	})
	assert.Nil(suite.T(), err)
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

func (suite *TestSuite) TestInjectByTypeNoCandidatesMandatory() {
	type OtherBean struct {
	}
	type SingletonBean struct {
		OtherBean *OtherBean `di.inject:""`
	}
	overwritten, err := RegisterBean("singletonBean", reflect.TypeOf((*SingletonBean)(nil)))
	assert.False(suite.T(), overwritten)
	assert.NoError(suite.T(), err)
	expectedError := errors.New("no candidates found for the injection")
	err = InitializeContainer()
	if assert.Error(suite.T(), err) {
		assert.Equal(suite.T(), expectedError, err)
	}
}

func (suite *TestSuite) TestInjectByTypeNoCandidatesOptional() {
	type OtherBean struct {
	}
	type SingletonBean struct {
		OtherBean *OtherBean `di.inject:"" di.optional:"true"`
	}
	overwritten, err := RegisterBean("singletonBean", reflect.TypeOf((*SingletonBean)(nil)))
	assert.False(suite.T(), overwritten)
	assert.NoError(suite.T(), err)
	err = InitializeContainer()
	assert.NoError(suite.T(), err)
	instance, err := GetInstanceSafe("singletonBean")
	assert.NoError(suite.T(), err)
	assert.Nil(suite.T(), instance.(*SingletonBean).OtherBean)
}

func (suite *TestSuite) TestInjectByTypeMoreThanOneCandidate() {
	type OtherBean struct {
	}
	type SingletonBean struct {
		RequestBean *OtherBean `di.inject:""`
	}
	overwritten, err := RegisterBean("singletonBean", reflect.TypeOf((*SingletonBean)(nil)))
	assert.False(suite.T(), overwritten)
	assert.NoError(suite.T(), err)
	overwritten, err = RegisterBeanInstance("candidate1", &OtherBean{})
	assert.False(suite.T(), overwritten)
	assert.NoError(suite.T(), err)
	overwritten, err = RegisterBeanInstance("candidate2", &OtherBean{})
	assert.False(suite.T(), overwritten)
	assert.NoError(suite.T(), err)
	expectedError := errors.New("more then one candidate found for the injection")
	err = InitializeContainer()
	if assert.Error(suite.T(), err) {
		assert.Equal(suite.T(), expectedError, err)
	}
}

func (suite *TestSuite) TestInjectByTypeWithType() {
	type OtherBean struct {
	}
	type SingletonBean struct {
		OtherBean *OtherBean `di.inject:""`
	}
	overwritten, err := RegisterBean("singletonBean", reflect.TypeOf((*SingletonBean)(nil)))
	assert.False(suite.T(), overwritten)
	assert.NoError(suite.T(), err)
	overwritten, err = RegisterBean("otherBean", reflect.TypeOf((*OtherBean)(nil)))
	assert.False(suite.T(), overwritten)
	assert.NoError(suite.T(), err)
	err = InitializeContainer()
	assert.NoError(suite.T(), err)
	instance, err := GetInstanceSafe("singletonBean")
	assert.NoError(suite.T(), err)
	assert.NotNil(suite.T(), instance.(*SingletonBean).OtherBean)
}

type someInterface interface {
	someMethod()
}

type otherBean struct {
}

func (otherBean) someMethod() {
}

func (suite *TestSuite) TestInjectByTypeWithInterface() {
	type SingletonBean struct {
		otherBean someInterface `di.inject:""`
	}
	overwritten, err := RegisterBean("singletonBean", reflect.TypeOf((*SingletonBean)(nil)))
	assert.False(suite.T(), overwritten)
	assert.NoError(suite.T(), err)
	overwritten, err = RegisterBean("otherBean", reflect.TypeOf((*otherBean)(nil)))
	assert.False(suite.T(), overwritten)
	assert.NoError(suite.T(), err)
	err = InitializeContainer()
	assert.NoError(suite.T(), err)
	instance, err := GetInstanceSafe("singletonBean")
	assert.NoError(suite.T(), err)
	assert.NotNil(suite.T(), instance.(*SingletonBean).otherBean)
}

func (suite *TestSuite) TestInjectByIDWithInterface() {
	type SingletonBean struct {
		otherBean someInterface `di.inject:"otherBean"`
	}
	overwritten, err := RegisterBean("singletonBean", reflect.TypeOf((*SingletonBean)(nil)))
	assert.False(suite.T(), overwritten)
	assert.NoError(suite.T(), err)
	overwritten, err = RegisterBean("otherBean", reflect.TypeOf((*otherBean)(nil)))
	assert.False(suite.T(), overwritten)
	assert.NoError(suite.T(), err)
	err = InitializeContainer()
	assert.NoError(suite.T(), err)
	instance, err := GetInstanceSafe("singletonBean")
	assert.NoError(suite.T(), err)
	assert.NotNil(suite.T(), instance.(*SingletonBean).otherBean)
}

func (suite *TestSuite) TestInjectToSliceWithTypeNoCandidatesNotOptional() {
	type OtherBean struct {
	}
	type SingletonBean struct {
		OtherBeans []*OtherBean `di.inject:""`
	}
	overwritten, err := RegisterBean("singletonBean", reflect.TypeOf((*SingletonBean)(nil)))
	assert.False(suite.T(), overwritten)
	assert.NoError(suite.T(), err)
	err = InitializeContainer()
	assert.NoError(suite.T(), err)
	instance, err := GetInstanceSafe("singletonBean")
	assert.NoError(suite.T(), err)
	assert.NotNil(suite.T(), instance.(*SingletonBean).OtherBeans)
	assert.Len(suite.T(), instance.(*SingletonBean).OtherBeans, 0)
}

func (suite *TestSuite) TestInjectToSliceWithTypeNoCandidatesOptional() {
	type OtherBean struct {
	}
	type SingletonBean struct {
		OtherBeans []*OtherBean `di.inject:"" di.optional:"true"`
	}
	overwritten, err := RegisterBean("singletonBean", reflect.TypeOf((*SingletonBean)(nil)))
	assert.False(suite.T(), overwritten)
	assert.NoError(suite.T(), err)
	err = InitializeContainer()
	assert.NoError(suite.T(), err)
	instance, err := GetInstanceSafe("singletonBean")
	assert.NoError(suite.T(), err)
	assert.Nil(suite.T(), instance.(*SingletonBean).OtherBeans)
}

func (suite *TestSuite) TestInjectToSliceWithType() {
	type OtherBean struct {
	}
	type SingletonBean struct {
		OtherBeans []*OtherBean `di.inject:""`
	}
	overwritten, err := RegisterBean("singletonBean", reflect.TypeOf((*SingletonBean)(nil)))
	assert.False(suite.T(), overwritten)
	assert.NoError(suite.T(), err)
	overwritten, err = RegisterBean("otherBean", reflect.TypeOf((*OtherBean)(nil)))
	assert.False(suite.T(), overwritten)
	assert.NoError(suite.T(), err)
	err = InitializeContainer()
	assert.NoError(suite.T(), err)
	instance, err := GetInstanceSafe("singletonBean")
	assert.NoError(suite.T(), err)
	assert.NotNil(suite.T(), instance.(*SingletonBean).OtherBeans)
	assert.Len(suite.T(), instance.(*SingletonBean).OtherBeans, 1)
}

func (suite *TestSuite) TestInjectToSliceWithInterface() {
	type SingletonBean struct {
		otherBeans []someInterface `di.inject:""`
	}
	overwritten, err := RegisterBean("singletonBean", reflect.TypeOf((*SingletonBean)(nil)))
	assert.False(suite.T(), overwritten)
	assert.NoError(suite.T(), err)
	overwritten, err = RegisterBean("otherBean", reflect.TypeOf((*otherBean)(nil)))
	assert.False(suite.T(), overwritten)
	assert.NoError(suite.T(), err)
	err = InitializeContainer()
	assert.NoError(suite.T(), err)
	instance, err := GetInstanceSafe("singletonBean")
	assert.NoError(suite.T(), err)
	assert.NotNil(suite.T(), instance.(*SingletonBean).otherBeans)
	assert.Len(suite.T(), instance.(*SingletonBean).otherBeans, 1)
}

func (suite *TestSuite) TestInjectToMapWithTypeNoCandidatesNotOptional() {
	type OtherBean struct {
	}
	type SingletonBean struct {
		OtherBeans map[string]*OtherBean `di.inject:""`
	}
	overwritten, err := RegisterBean("singletonBean", reflect.TypeOf((*SingletonBean)(nil)))
	assert.False(suite.T(), overwritten)
	assert.NoError(suite.T(), err)
	err = InitializeContainer()
	assert.NoError(suite.T(), err)
	instance, err := GetInstanceSafe("singletonBean")
	assert.NoError(suite.T(), err)
	assert.NotNil(suite.T(), instance.(*SingletonBean).OtherBeans)
	assert.Len(suite.T(), instance.(*SingletonBean).OtherBeans, 0)
}

func (suite *TestSuite) TestInjectToMapWithTypeNoCandidatesOptional() {
	type OtherBean struct {
	}
	type SingletonBean struct {
		OtherBeans map[string]*OtherBean `di.inject:"" di.optional:"true"`
	}
	overwritten, err := RegisterBean("singletonBean", reflect.TypeOf((*SingletonBean)(nil)))
	assert.False(suite.T(), overwritten)
	assert.NoError(suite.T(), err)
	err = InitializeContainer()
	assert.NoError(suite.T(), err)
	instance, err := GetInstanceSafe("singletonBean")
	assert.NoError(suite.T(), err)
	assert.Nil(suite.T(), instance.(*SingletonBean).OtherBeans)
}

func (suite *TestSuite) TestInjectToMapWithType() {
	type OtherBean struct {
	}
	type SingletonBean struct {
		OtherBeans map[string]*OtherBean `di.inject:""`
	}
	overwritten, err := RegisterBean("singletonBean", reflect.TypeOf((*SingletonBean)(nil)))
	assert.False(suite.T(), overwritten)
	assert.NoError(suite.T(), err)
	overwritten, err = RegisterBean("otherBean", reflect.TypeOf((*OtherBean)(nil)))
	assert.False(suite.T(), overwritten)
	assert.NoError(suite.T(), err)
	err = InitializeContainer()
	assert.NoError(suite.T(), err)
	instance, err := GetInstanceSafe("singletonBean")
	assert.NoError(suite.T(), err)
	assert.NotNil(suite.T(), instance.(*SingletonBean).OtherBeans)
	assert.Len(suite.T(), instance.(*SingletonBean).OtherBeans, 1)
	assert.NotNil(suite.T(), instance.(*SingletonBean).OtherBeans["otherBean"])
}

func (suite *TestSuite) TestInjectToMapWithInterface() {
	type SingletonBean struct {
		otherBeans map[string]someInterface `di.inject:""`
	}
	overwritten, err := RegisterBean("singletonBean", reflect.TypeOf((*SingletonBean)(nil)))
	assert.False(suite.T(), overwritten)
	assert.NoError(suite.T(), err)
	overwritten, err = RegisterBean("otherBean", reflect.TypeOf((*otherBean)(nil)))
	assert.False(suite.T(), overwritten)
	assert.NoError(suite.T(), err)
	err = InitializeContainer()
	assert.NoError(suite.T(), err)
	instance, err := GetInstanceSafe("singletonBean")
	assert.NoError(suite.T(), err)
	assert.NotNil(suite.T(), instance.(*SingletonBean).otherBeans)
	assert.Len(suite.T(), instance.(*SingletonBean).otherBeans, 1)
	assert.NotNil(suite.T(), instance.(*SingletonBean).otherBeans["otherBean"])
}

func (suite *TestSuite) TestInjectRequestBean() {
	type RequestBean struct {
		Scope Scope `di.scope:"request"`
	}
	type SingletonBean struct {
		RequestBean *RequestBean `di.inject:"requestBean"`
	}
	overwritten, err := RegisterBean("singletonBean", reflect.TypeOf((*SingletonBean)(nil)))
	assert.False(suite.T(), overwritten)
	assert.NoError(suite.T(), err)
	overwritten, err = RegisterBean("requestBean", reflect.TypeOf((*RequestBean)(nil)))
	assert.False(suite.T(), overwritten)
	assert.NoError(suite.T(), err)
	expectedError := errors.New(requestScopedBeansCantBeInjected)
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
	overwritten, err := RegisterBeanFactory("requestBean", Request, func(context.Context) (interface{}, error) {
		return nil, errors.New("cannot initialize request bean")
	})
	assert.False(suite.T(), overwritten)
	assert.NoError(suite.T(), err)
	err = InitializeContainer()
	assert.NoError(suite.T(), err)
	assert.Panics(suite.T(), func() {
		getRequestBeanInstance(context.Background(), "requestBean")
	})
}

type contextAwareBean struct {
	ctx context.Context
}

func (cab *contextAwareBean) SetContext(ctx context.Context) {
	cab.ctx = ctx
}

func (suite *TestSuite) TestContextAwareBean() {
	overwritten, err := RegisterBean("contextAwareBean", reflect.TypeOf((*contextAwareBean)(nil)))
	assert.False(suite.T(), overwritten)
	assert.NoError(suite.T(), err)
	err = InitializeContainer()
	assert.NoError(suite.T(), err)
	instance, err := GetInstanceSafe("contextAwareBean")
	assert.NoError(suite.T(), err)
	assert.NotNil(suite.T(), instance)
	assert.Equal(suite.T(), context.Background(), instance.(*contextAwareBean).ctx)
}

func (suite *TestSuite) TestContextAwareBeanFactory() {
	var outerCtx context.Context
	overwritten, err := RegisterBeanFactory("beanId", Singleton, func(ctx context.Context) (interface{}, error) {
		outerCtx = ctx
		return new(string), nil
	})
	assert.False(suite.T(), overwritten)
	assert.Nil(suite.T(), err)
	err = InitializeContainer()
	assert.False(suite.T(), overwritten)
	assert.Nil(suite.T(), err)
	instance, err := GetInstanceSafe("beanId")
	assert.Nil(suite.T(), err)
	assert.NotNil(suite.T(), instance)
	assert.Equal(suite.T(), context.Background(), outerCtx)
}

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
	overwritten, err = RegisterBeanFactory("beanFactory", Request, func(context.Context) (interface{}, error) {
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

var closedSingletons []bool

type SingletonBeanWithClose struct {
}

func (*SingletonBeanWithClose) Close() error {
	closedSingletons = append(closedSingletons, true)
	return nil
}

type SingletonBeanWithErrorOnClose struct {
}

var singletonBeansWithErrorOnClose []error = nil

func (*SingletonBeanWithErrorOnClose) Close() error {
	err := errors.New("cannot close the bean")
	singletonBeansWithErrorOnClose = append(singletonBeansWithErrorOnClose, err)
	return err
}

func (suite *TestSuite) TestShutdown() {
	bean, err := RegisterBean("singletonBean", reflect.TypeOf((*SingletonBeanWithClose)(nil)))
	assert.NoError(suite.T(), err)
	assert.NotNil(suite.T(), bean)
	err = InitializeContainer()
	assert.NoError(suite.T(), err)
	assert.Equal(suite.T(), 0, len(closedSingletons))
	Close()
	assert.True(suite.T(), closedSingletons[0])
}

func (suite *TestSuite) TestShutdownNothingToClose() {
	type SingletonBean struct {
	}
	bean, err := RegisterBean("singletonBean", reflect.TypeOf((*SingletonBean)(nil)))
	assert.NoError(suite.T(), err)
	assert.NotNil(suite.T(), bean)
	err = InitializeContainer()
	assert.NoError(suite.T(), err)
	Close()
}

func (suite *TestSuite) TestShutdownErrorOnClose() {
	_, _ = RegisterBean("singletonBeanWithErrorOnClose", reflect.TypeOf((*SingletonBeanWithErrorOnClose)(nil)))
	err := InitializeContainer()
	assert.NoError(suite.T(), err)
	assert.Nil(suite.T(), singletonBeansWithErrorOnClose)
	Close()
	assert.Equal(suite.T(), errors.New("cannot close the bean"), singletonBeansWithErrorOnClose[0])
}

func (suite *TestSuite) TestShutdownContinueOnError() {
	//cause of map usage internally in RegisterBean and order is unknown
	for i := 1; i < 10; i++ {
		_, _ = RegisterBean("a"+strconv.Itoa(i), reflect.TypeOf((*SingletonBeanWithClose)(nil)))
		i++
		_, _ = RegisterBean("a"+strconv.Itoa(i), reflect.TypeOf((*SingletonBeanWithErrorOnClose)(nil)))
	}
	err := InitializeContainer()
	assert.NoError(suite.T(), err)
	assert.Equal(suite.T(), 0, len(closedSingletons))
	assert.Equal(suite.T(), 0, len(singletonBeansWithErrorOnClose))
	Close()
	assert.Equal(suite.T(), 5, len(closedSingletons))
	assert.Equal(suite.T(), 5, len(singletonBeansWithErrorOnClose))
}

func (suite *TestSuite) TestInjectInParent() {
	type SingletonBeanParent struct {
		otherBean1 someInterface `di.inject:""`
	}
	type SingletonBeanChild struct {
		SingletonBeanParent
		otherBean2 someInterface `di.inject:""`
	}
	
	overwritten, err := RegisterBean("singletonBean", reflect.TypeOf((*SingletonBeanChild)(nil)))
	assert.False(suite.T(), overwritten)
	assert.NoError(suite.T(), err)
	overwritten, err = RegisterBean("otherBean", reflect.TypeOf((*otherBean)(nil)))
	assert.False(suite.T(), overwritten)
	assert.NoError(suite.T(), err)
	err = InitializeContainer()
	assert.NoError(suite.T(), err)
	instance, err := GetInstanceSafe("singletonBean")
	assert.NoError(suite.T(), err)
	assert.NotNil(suite.T(), instance.(*SingletonBeanChild).otherBean1)
	assert.NotNil(suite.T(), instance.(*SingletonBeanChild).otherBean2)
}