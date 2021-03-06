# goioc/di: Dependency Injection
[![goioc](https://habrastorage.org/webt/ym/pu/dc/ympudccm7j7a3qex_jjroxgsiwg.png)](https://github.com/goioc)

[![Go](https://github.com/goioc/di/workflows/Go/badge.svg)](https://github.com/goioc/di/actions)
[![go.dev reference](https://img.shields.io/badge/go.dev-reference-007d9c?logo=go&logoColor=white&style=flat-square)](https://pkg.go.dev/github.com/goioc/di/?tab=doc)
[![CodeFactor](https://www.codefactor.io/repository/github/goioc/di/badge)](https://www.codefactor.io/repository/github/goioc/di)
[![Go Report Card](https://goreportcard.com/badge/github.com/goioc/di)](https://goreportcard.com/report/github.com/goioc/di)
[![codecov](https://codecov.io/gh/goioc/di/branch/master/graph/badge.svg)](https://codecov.io/gh/goioc/di)
[![Quality Gate Status](https://sonarcloud.io/api/project_badges/measure?project=goioc_di&metric=alert_status)](https://sonarcloud.io/dashboard?id=goioc_di)
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
	weather, _ := wc.weatherService.Weather(r.URL.Query().Get("city"))
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

Of course, for such a simple example it may look like an overkill. But for larger projects with many interconnected services with complicated business logic, it can really simplify your life!

## Looks nice... Give me some details!

The main component of the library is the [Inversion of Control Container](https://www.martinfowler.com/articles/injection.html) that contains and manages instances of your structures (called "beans").

### Types of beans

- **Singleton**. Exists only in one copy in the container. Every time you retrieve the instance from the container (or every time it's being injected to another bean) - it will be the same instance.
- **Prototype**. It can exist in multiple copies: a new copy is created upon retrieval from the container (or upon injection into another bean).
- **Request**. Similar to `Prototype`, however it has a few differences and features (since its lifecycle is bound to a web request):
   - Can't be injected to other beans.
   - Can't be manually retrieved from the Container.
   - `Request` beans are automatically injected to the `context.Context` of a corresponding `http.Request`. 
   - If a `Request` bean implements `io.Closer`, it will be "closed" upon corresponding request's cancellation.

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
		return "My awesome string that is going to become a bean!", nil
	})
```
Feel free to use any scope with this method. By the way, you can even lookup other beans within the factory:
```go
di.RegisterBeanFactory("beanID", Singleton, func() (interface{}, error) {
		return di.GetInstance("someOtherBeanID"), nil
	})
```

### Beans initialization

There's a special interface `InitializingBean` that can be implemented to provide your bean with some initialization logic that will we executed after the container is initialized (for `Singleton` beans) or after the `Prototype`/`Request` instance is created. Again, you can also lookup other beans during initialization (since the container is ready by that time):

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

### Beans post-processors

The alternative way of initializing beans is using so-called "beans post-processors". Take a look at the example:

```go
type postprocessedBean struct {
	a string
	b string
}

_, _ := RegisterBean("postprocessedBean", reflect.TypeOf((*postprocessedBean)(nil)))

_ = RegisterBeanPostprocessor(reflect.TypeOf((*postprocessedBean)(nil)), func(instance interface{}) error {
    instance.(*postprocessedBean).a = "Hello, "
    return nil
})

_ = RegisterBeanPostprocessor(reflect.TypeOf((*postprocessedBean)(nil)), func(instance interface{}) error {
instance.(*postprocessedBean).b = "world!"
    return nil
})

_ = InitializeContainer()

instance := GetInstance("postprocessedBean")

postprocessedBean := instance.(*postprocessedBean)
println(postprocessedBean.a+postprocessedBean.b) // prints out "Hello, world!"
```

### Beans injection

As was mentioned above, one bean can be injected into another with the `PostConstruct` method. However, the more handy way of doing it is by using a special tag:

```go
type SingletonBean struct {
	SomeOtherBean *SomeOtherBean `di.inject:"someOtherBean"`
}
```

... or via interface ...

```go
type SingletonBean struct {
	SomeOtherBean SomeOtherBeansInterface `di.inject:"someOtherBean"`
}
```

Note that you can refer dependencies either by pointer, or by interface, but not by value. And just a reminder: you can't inject `Request` beans.

Sometimes we might want to have optional dependencies. By default, all declared dependencies are considered to be required: if some dependency is not found in the Container, you will get an error. However, you can specify an optional dependency like this:

```go
type SingletonBean struct {
	SomeOtherBean *string `di.inject:"someOtherBean" di.optional:"true"`
}
```

In this case, if `someOtherBean` is not found in the Container, you will get `nill` injected into this field.

Finally, you don't need a bean ID to preform an injection! Check this out:

```go
type SingletonBean struct {
	SomeOtherBean *string `di.inject:""`
}
```

In this case, DI will try to find a candidate for the injection automatically (among registered beans of type `*string`). Cool, ain't it? 🤠
It will panic though if no candidates are found (and if the dependency is not marked as optional), or if there is more than one candidates found. 

### Circular dependencies

The problem with all IoC containers is that beans' interconnection may suffer from so-called circular dependencies. Consider this example:

```go
type CircularBean struct {
	Scope        Scope         `di.scope:"prototype"`
	CircularBean *CircularBean `di.inject:"circularBean"`
}
```

Trying to use such bean will result in the `circular dependency detected for bean: circularBean` error. There's no problem as such with referencing a bean from itself - if it's a `Singleton` bean. But doing it with `Prototype`/`Request` beans will lead to infinite creation of the instances. So, be careful with this: "with great power comes great responsibility" 🕸 

## What about middleware?

We have some 😎 Here's an example with [gorilla/mux](https://github.com/gorilla/mux) router (but feel free to use any other router). 
Basically, it's an extension of the very first example with the weather controller, but this time we add `Request` beans and access them via request's context. 
Also, this example demonstrates how DI can automatically close resources for you (DB connection in this case). The proper error handling is, again, omitted for simplicity.

**controllers/weather_controller.go**
```go
package controllers

import (
	"database/sql"
	"di-demo/services"
	"github.com/goioc/di"
	"net/http"
)

type WeatherController struct {
	// note that injection works even with unexported fields
	weatherService *services.WeatherService `di.inject:"weatherService"`
}

func (wc *WeatherController) Weather(w http.ResponseWriter, r *http.Request) {
	dbConnection := r.Context().Value(di.BeanKey("dbConnection")).(*sql.Conn)
	city := r.URL.Query().Get("city")
	_, _ = dbConnection.ExecContext(r.Context(), "insert into log values (?, ?, datetime('now'))", city, r.RemoteAddr)
	weather, _ := wc.weatherService.Weather(city)
	_, _ = w.Write([]byte(*weather))
}
```

**controllers/index_controller.go**
```go
package controllers

import (
	"database/sql"
	"fmt"
	"github.com/goioc/di"
	"net/http"
	"strings"
	"time"
)

type IndexController struct {
}

func (ic *IndexController) Log(w http.ResponseWriter, r *http.Request) {
	dbConnection := r.Context().Value(di.BeanKey("dbConnection")).(*sql.Conn)
	rows, _ := dbConnection.QueryContext(r.Context(), "select * from log")
	columns, _ := rows.Columns()
	_, _ = w.Write([]byte(strings.ToUpper(fmt.Sprintf("Requests log: %v\n\n", columns))))
	for rows.Next() {
		var city string
		var ip string
		var dateTime time.Time
		_ = rows.Scan(&city, &ip, &dateTime)
		_, _ = w.Write([]byte(fmt.Sprintln(city, "\t", ip, "\t", dateTime)))
	}
}
```

**init.go**
```go
package main

import (
	"context"
	"database/sql"
	"di-demo/controllers"
	"di-demo/services"
	"github.com/goioc/di"
	"os"
	"reflect"
)

func init() {
	_, _ = di.RegisterBean("weatherService", reflect.TypeOf((*services.WeatherService)(nil)))
	_, _ = di.RegisterBean("indexController", reflect.TypeOf((*controllers.IndexController)(nil)))
	_, _ = di.RegisterBean("weatherController", reflect.TypeOf((*controllers.WeatherController)(nil)))
	_, _ = di.RegisterBeanFactory("db", di.Singleton, func() (interface{}, error) {
		_ = os.Remove("./di-demo.db")
		db, _ := sql.Open("sqlite3", "./di-demo.db")
		db.SetMaxOpenConns(1)
		_, _ = db.Exec("create table log ('city' varchar not null, 'ip' varchar not null, 'time' datetime not null)")
		return db, nil
	})
	_, _ = di.RegisterBeanFactory("dbConnection", di.Request, func() (interface{}, error) {
		db, _ := di.GetInstanceSafe("db")
		return db.(*sql.DB).Conn(context.TODO())
	})
	_ = di.InitializeContainer()
}
```

**main.go**
```go
package main

import (
	"di-demo/controllers"
	"github.com/goioc/di"
	"github.com/gorilla/mux"
	_ "github.com/mattn/go-sqlite3"
	"net/http"
)

func main() {
	router := mux.NewRouter()
	router.Use(di.Middleware)
	router.Path("/").HandlerFunc(di.GetInstance("indexController").(*controllers.IndexController).Log)
	router.Path("/weather").Queries("city", "{*?}").HandlerFunc(di.GetInstance("weatherController").(*controllers.WeatherController).Weather)
	_ = http.ListenAndServe(":8080", router)
}
```

## Okaaay... More examples?

Please, take a look at the [unit-tests](https://github.com/goioc/di/blob/master/di_test.go) for more examples.
