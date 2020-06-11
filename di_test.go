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

func TestExampleTestSuite(t *testing.T) {
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
	overwritten, err = RegisterBeanFactory("", Singleton, nil)
	assert.False(suite.T(), overwritten)
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

func (suite *TestSuite) TestRegisterBeanWithOverwriting() {
	overwritten, err := RegisterBeanInstance("bean", new(string))
	assert.False(suite.T(), overwritten)
	assert.NoError(suite.T(), err)
	overwritten, err = RegisterBeanInstance("bean", new(string))
	assert.True(suite.T(), overwritten)
	assert.NoError(suite.T(), err)
	err = InitializeContainer()
	assert.NoError(suite.T(), err)
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

type FailingBean struct {
}

func (fb *FailingBean) PostConstruct() error {
	return errors.New("error message")
}

func (suite *TestSuite) TestPostConstructReturnsError() {
	overwritten, err := RegisterBean("failingBean", reflect.TypeOf((*FailingBean)(nil)))
	assert.False(suite.T(), overwritten)
	assert.NoError(suite.T(), err)
	expectedError := errors.New("error message")
	err = InitializeContainer()
	if assert.Error(suite.T(), err) {
		assert.Equal(suite.T(), expectedError, err)
	}
}

type PostConstructBean1 struct {
	Value string
}

func (pcb *PostConstructBean1) PostConstruct() error {
	pcb.Value = "some content"
	return nil
}

type PostConstructBean2 struct {
	Scope              Scope `di.scope:"prototype"`
	PostConstructBean1 *PostConstructBean1
}

func (pcb *PostConstructBean2) PostConstruct() error {
	instance, err := GetInstanceSafe("postConstructBean1")
	if err != nil {
		return err
	}
	pcb.PostConstructBean1 = instance.(*PostConstructBean1)
	return nil
}

type PostConstructBean3 struct {
	PostConstructBean2 *PostConstructBean2
}

func (pcb *PostConstructBean3) PostConstruct() error {
	instance, err := GetInstanceSafe("postConstructBean2")
	if err != nil {
		return err
	}
	pcb.PostConstructBean2 = instance.(*PostConstructBean2)
	return nil
}

func (suite *TestSuite) TestPostConstruct() {
	overwritten, err := RegisterBean("postConstructBean1", reflect.TypeOf((*PostConstructBean1)(nil)))
	assert.False(suite.T(), overwritten)
	assert.NoError(suite.T(), err)
	overwritten, err = RegisterBean("postConstructBean2", reflect.TypeOf((*PostConstructBean2)(nil)))
	assert.False(suite.T(), overwritten)
	assert.NoError(suite.T(), err)
	overwritten, err = RegisterBean("postConstructBean3", reflect.TypeOf((*PostConstructBean3)(nil)))
	assert.False(suite.T(), overwritten)
	assert.NoError(suite.T(), err)
	err = InitializeContainer()
	assert.NoError(suite.T(), err)
	assert.Equal(suite.T(), "some content", GetInstance("postConstructBean3").(*PostConstructBean3).PostConstructBean2.PostConstructBean1.Value)
}

type CircularBean struct {
	Scope        Scope         `di.scope:"prototype"`
	CircularBean *CircularBean `di.inject:"circularBean"`
}

func (suite *TestSuite) TestDirectCircularDependency() {
	overwritten, err := RegisterBean("circularBean", reflect.TypeOf((*CircularBean)(nil)))
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

func (suite *TestSuite) TestIndirectCircularDependency() {
	type SingletonBean struct {
		CircularBean *CircularBean `di.inject:"circularBean"`
	}
	overwritten, err := RegisterBean("singletonBean", reflect.TypeOf((*SingletonBean)(nil)))
	assert.False(suite.T(), overwritten)
	assert.NoError(suite.T(), err)
	overwritten, err = RegisterBean("circularBean", reflect.TypeOf((*CircularBean)(nil)))
	assert.False(suite.T(), overwritten)
	assert.NoError(suite.T(), err)
	expectedError := errors.New("circular dependency detected for bean: circularBean")
	err = InitializeContainer()
	if assert.Error(suite.T(), err) {
		assert.Equal(suite.T(), expectedError, err)
	}
}
