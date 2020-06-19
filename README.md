# goioc/di: Dependency Injection
![](https://habrastorage.org/webt/ym/pu/dc/ympudccm7j7a3qex_jjroxgsiwg.png)

![Go](https://github.com/goioc/di/workflows/Go/badge.svg)
[![go.dev reference](https://img.shields.io/badge/go.dev-reference-007d9c?logo=go&logoColor=white&style=flat-square)](https://pkg.go.dev/github.com/goioc/di/?tab=doc)
[![CodeFactor](https://www.codefactor.io/repository/github/goioc/di/badge)](https://www.codefactor.io/repository/github/goioc/di)
[![Go Report Card](https://goreportcard.com/badge/github.com/goioc/di)](https://goreportcard.com/report/github.com/goioc/di)
[![codecov](https://codecov.io/gh/goioc/di/branch/master/graph/badge.svg)](https://codecov.io/gh/goioc/di)
[![Dependabot Status](https://api.dependabot.com/badges/status?host=github&repo=goioc/di)](https://dependabot.com)
[![DeepSource](https://static.deepsource.io/deepsource-badge-light-mini.svg)](https://deepsource.io/gh/goioc/di/?ref=repository-badge)

## Why DI in Go? Why IoC at all?
I've been using Dependency Injection in Java for nearly 10 years via [Spring Framework](https://spring.io/). I'm not saying that one can't live without it, but it's proven to be very useful for large enterprise-level applications. You may argue that Go follows a completely different ideology, values different principles and paradigms than Java, and DI is not needed in this better world. And I can even partly agree with that. And yet I decided to create this light-weight Spring-like library for Go. You are free to not use it, after all 🙂

## Is it the only DI library for Go?
No, of course not. There's a bunch of libraries around which serve a similar purpose (I even took inspiration from some of them). The problem is that I was missing something in all of these libraries... Therefore I decided to create Yet Another IoC Container that would rule them all. You are more than welcome to use any other library, for example [this nice project](https://github.com/sarulabs/di). And still, I'd recommend stopping by here 😉

## So, how does it work? 
It's better to show than to describe. Take a look at this toy-example (error-handling is omitted to minimize code snippets):

**services/weather_service.go**
```go
package services

import (
	"io/ioutil"
	"net/http"
)

type WeatherService struct {
}

func (ws *WeatherService) Weather(city string) (*string, error) {
	response, err := http.Get("https://wttr.in/" + city)
	if err != nil {
		return nil, err
	}
	all, err := ioutil.ReadAll(response.Body)
	if err != nil {
		return nil, err
	}
	weather := string(all)
	return &weather, nil
}
```


**controllers/weather_controller.go**
```go
package controllers

import (
	"di-demo/services"
	"github.com/goioc/di"
	"net/http"
)

type WeatherController struct {
	// note that injection works even with unexported fields
	weatherService *services.WeatherService `di.inject:"weatherService"`
}

func (wc *WeatherController) Weather(w http.ResponseWriter, r *http.Request) {
	cities, ok := r.URL.Query()["city"]
	if !ok || len(cities) < 1 {
		_, _ = w.Write([]byte("required parameter 'city' is missing"))
		return
	}
	weather, _ := wc.weatherService.Weather(cities[0])
	_, _ = w.Write([]byte(*weather))
}
```

**init.go**
```go
package main

import (
	"di-demo/controllers"
	"di-demo/services"
	"github.com/goioc/di"
	"reflect"
)

func init() {
	_, _ = di.RegisterBean("weatherService", reflect.TypeOf((*services.WeatherService)(nil)))
	_, _ = di.RegisterBean("weatherController", reflect.TypeOf((*controllers.WeatherController)(nil)))
	_ = di.InitializeContainer()
}
```

**main.go**
```go
package main

import (
	"di-demo/controllers"
	"github.com/goioc/di"
	"net/http"
)

func main() {
	http.HandleFunc("/weather", func(w http.ResponseWriter, r *http.Request) {
		di.GetInstance("weatherController").(*controllers.WeatherController).Weather(w, r)
	})
	_ = http.ListenAndServe(":8080", nil)
}
```

If you run it, you should be able to observe a neat weather forecast at http://localhost:8080/weather?city=London (or for any other city).

Of course, for such a simple example it may look like an overkill. But for larger projects with many interconnected services with complicated business-logic, it can really simplify your life!

## Looks nice... Give me some details!

The main component of the library is the [Inversion of Control Container](https://www.martinfowler.com/articles/injection.html) that contains and manages instances of your structures (called "beans").

### Types of beans

- **Singleton**. Exists only in one copy in the container. Every time you retrieve the instance from the container (or every time it's being injected to another bean) - it will be the same instance.
- **Prototype**. It can exist in multiple copies: new copy is created upon retrieval from the container (or upon injection into another bean).

### Beans registration

For the container to become aware of the beans, one must register them manually (unlike Java, unfortunately, we can't scan classpath to do it automatically, because Go runtime doesn't contain high-level information about types). How can one register beans in the container?

- **By type**. This is described in the example above. A structure is declared with a field tagged with `di.scope:"<scope>"`. This field can be even omitted - in this case, the default scope will be `Singleton`. Than the registration is done like this:
```go
di.RegisterBean("beanID", reflect.TypeOf((*YourAwesomeStructure)(nil)))
```

- **Using pre-created instance**. What if you already have an instance that you want to register as a bean? You can do it like this:
```go
di.RegisterBeanInstance("beanID", yourAwesomeInstance)
```
For this type of beans, the only supported scope is `Singleton`, because I don't dare to clone your instances to enable prototyping 😅

- **Via bean factory**. If you have a method that is producing instances for you, you can register it as a bean factory:
```go
di.RegisterBeanFactory("beanID", Singleton, func() (interface{}, error) {
		return "My awesome string that is go to become a bean!", nil
	})
```
Feel free to use any scope with this method. By the way, you can even lookup other beans within the factory:
```go
di.RegisterBeanFactory("beanID", Singleton, func() (interface{}, error) {
		return di.GetInstance("someOtherBeanID"), nil
	})
```

### Beans initialization

There's a special interface `InitializingBean` that can be implemented to provide your bean with some initialization logic that will we executed after the container is initialized (for `Singleton` beans) or after the `Prototype` instance is created. Again, you can also lookup other beans during initialization (since the container is ready by that time):

```go
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
	instance, err := di.GetInstanceSafe("postConstructBean1")
	if err != nil {
		return err
	}
	pcb.PostConstructBean1 = instance.(*PostConstructBean1)
	return nil
}
```


### Beans injection

As was mentioned above, one bean can be injected into another with the `PostConstruct` method. But the more handy way of doing it is by using a special tag:

```go
type SingletonBean struct {
	SomeOtherBean string `di.inject:"someOtherBean"`
}
```

### Circular dependencies

The problem with all IoC containers is that beans' interconnection may suffer from so-called circular dependencies. Consider this example:

```go
type CircularBean struct {
	Scope        Scope         `di.scope:"prototype"`
	CircularBean *CircularBean `di.inject:"circularBean"`
}
```

Trying to use such bean will result in the `circular dependency detected for bean: circularBean` error. There's no problem as such with referencing a bean from itself - if it's a `Singleton` bean. But doing it with `Prototype` beans will lead to infinite creation of the instances. So, be careful with this: "with great power comes great responsibility" 🕸 

## Okaaay... More examples?

Please, take a look at the [unit-tests](https://github.com/goioc/di/blob/master/di_test.go) for more examples.

## Is there something more coming?

Yes, definitely. The next thing to be implemented is a new scope: `Request`. This scope is, again, [inspired](https://docs.spring.io/spring-framework/docs/current/javadoc-api/org/springframework/web/context/request/RequestScope.html) by the Spring Framework: its life-cycle is bound to the HTTP request that is being processed. This scope is very useful for web-applications, but it's not essential for the IoC itself, so the development is slightly postponed (also, you can mimic `Request` scope even now with the combination of `Prototype` beans and/or bean factories). But stay tuned! 🤩
