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
I've been using Depencency Injection in Java for nearly 10 years via [Spring Framework](https://spring.io/). I'm not saying that one can't live without it, but it's proven to be very useful for large enterprise-level applications. You may argue that Go follows completely different principles and paradigms than Java and DI is not needed in this better world. And I can even partly agree with that. And yet I decided to create this light-weight Spring-like library for Go. You are free to not use it, after all :)

## Is it the only DI library for Go?
No, of course not. There's a bunch of libraries around which serve similar purpose (I even took inspiration from some of them). The problem is that I was missing something in all of these libraries... Therefore I decided to create Yet Another IoC Container that would rule them all. You are more than welccome to use any other library, for example [this nice project](https://github.com/sarulabs/di). And still, I'd recommend to stop by here :) 

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

If you run it, you should be able to observe neat weather forecast at http://localhost:8080/weather?city=London (or for any other city).

Of course, for such a simple example it may look like an overkill. But for larger projects with many inter-connected services with complicated business-logic it can really simplify your life!
