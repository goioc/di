# goioc/di: Dependency Injection
![](https://habrastorage.org/webt/ym/pu/dc/ympudccm7j7a3qex_jjroxgsiwg.png)

![Go](https://github.com/goioc/di/workflows/Go/badge.svg)
[![go.dev reference](https://img.shields.io/badge/go.dev-reference-007d9c?logo=go&logoColor=white&style=flat-square)](https://pkg.go.dev/github.com/goioc/di/v2?tab=doc)
[![CodeFactor](https://www.codefactor.io/repository/github/goioc/di/badge)](https://www.codefactor.io/repository/github/goioc/di)
[![Go Report Card](https://goreportcard.com/badge/github.com/goioc/di)](https://goreportcard.com/report/github.com/goioc/di)
[![codecov](https://codecov.io/gh/goioc/di/branch/master/graph/badge.svg)](https://codecov.io/gh/goioc/di)
[![Dependabot Status](https://api.dependabot.com/badges/status?host=github&repo=goioc/di)](https://dependabot.com)
[![DeepSource](https://static.deepsource.io/deepsource-badge-light-mini.svg)](https://deepsource.io/gh/goioc/di/?ref=repository-badge)

## Why DI in Go? Why IoC at all?
I've been using Dependency Injection in Java for nearly 10 years via [Spring Framework](https://spring.io/). I'm not saying that one can't live without it, but it's proven to be very useful for large enterprise-level applications. You may argue that Go follows a completely different ideology, values different principles and paradigms than Java, and DI is not needed in this better world. And I can even partly agree with that. And yet I decided to create this light-weight Spring-like library for Go. You are free to not use it, after all 🙂

## Is it the only DI library for Go?
No, of course not. There's a bunch of libraries around which serve a similar purpose (I even took inspiration from some of them). The problem is that I was missing something in all of these libraries... Therefore I decided to create Yet Another IoC Container that would rule them all. You are more than welcome to use any other library, for example [this nice project](https://github.com/sarulabs/di). And still, I'd recommend stopping by here 🙂

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
RegisterBeanInstance("beanID", yourAwesomeInstance)
```
For this type of beans, the only supported scope is `Singleton`, because I don't dare copy your instances to enable prototyping 😅

- **Via bean factory**. If you have a method that is producing instances for you, you can register it as a bean factory:
```go
RegisterBeanFactory("beanID", Singleton, func() (interface{}, error) {
		return "My awesome string that is go to become a bean!", nil
	})
```
Feel free to use any scope with this method.
