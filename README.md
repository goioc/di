# goioc/di: Dependency Injection
[![goioc](https://habrastorage.org/webt/ym/pu/dc/ympudccm7j7a3qex_jjroxgsiwg.png)](https://github.com/goioc)

[![Go](https://github.com/goioc/di/workflows/Go/badge.svg)](https://github.com/goioc/di/actions)
[![go.dev reference](https://img.shields.io/badge/go.dev-reference-007d9c?logo=go&logoColor=white&style=flat-square)](https://pkg.go.dev/github.com/goioc/di/?tab=doc)
[![codecov](https://codecov.io/gh/goioc/di/branch/master/graph/badge.svg)](https://codecov.io/gh/goioc/di)
[![Awesome](https://cdn.rawgit.com/sindresorhus/awesome/d7305f38d29fed78fa85652e3a63e154dd8e8829/media/badge.svg)](https://github.com/sindresorhus/awesome)

[![ko-fi](https://ko-fi.com/img/githubbutton_sm.svg)](https://ko-fi.com/G2G5JUKU7)

`di` is a process-wide dependency injection container for Go. Register your
application's components (called **beans**) by type, instance, or factory, then
initialize the container. It provides field injection, lifecycle hooks, and
singleton, prototype, and HTTP request scopes.

## Install

Requires Go 1.20 or later.

```sh
go get github.com/goioc/di
```

## Quick start

This complete example registers configuration and a service, injects the
configuration, and checks registration, startup, and lookup errors.

```go
package main

import (
	"fmt"
	"log"
	"reflect"

	"github.com/goioc/di"
)

type Config struct {
	Greeting string
}

type Greeter struct {
	Config *Config `di.inject:"config"`
}

func (g *Greeter) Greet(name string) string {
	return g.Config.Greeting + ", " + name + "!"
}

func run() error {
	defer di.Close()
	if _, err := di.RegisterBeanInstance("config", &Config{Greeting: "Hello"}); err != nil {
		return err
	}
	if _, err := di.RegisterBean("greeter", reflect.TypeOf((*Greeter)(nil))); err != nil {
		return err
	}
	if err := di.InitializeContainer(); err != nil {
		return err
	}
	instance, err := di.GetInstanceSafe("greeter")
	if err != nil {
		return err
	}
	fmt.Println(instance.(*Greeter).Greet("Go")) // Hello, Go!
	return nil
}

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}
```

Register everything before startup. Wait for `InitializeContainer()` to succeed
before starting application goroutines or serving requests. The container is
shared by the entire process; `di.Close()` clears it for a fresh registration cycle.

## Registration and scopes

| Registration | Accepted input | Scope | Field injection |
| --- | --- | --- | --- |
| `RegisterBean` | A pointer-to-struct `reflect.Type` | `di.scope` tag; defaults to singleton | Yes |
| `RegisterBeanInstance` | An existing non-nil pointer | Singleton | No |
| `RegisterBeanFactory` | A function returning a non-nil pointer and an error | Explicit argument | No |

All three forms receive lifecycle callbacks and registered postprocessors. A
valid registration replaces any existing registration with the same ID and
returns `overwritten == true`. Invalid input leaves the previous registration
intact. Registration and postprocessor changes are rejected during startup,
normal operation, rollback, and shutdown.

| Scope | Creation | Cleanup after successful initialization |
| --- | --- | --- |
| `di.Singleton` | Once during startup; reused by lookups and injection | `di.Close()` calls `io.Closer.Close`, if implemented |
| `di.Prototype` | A new instance for each lookup or injection | The caller or consuming bean owns cleanup |
| `di.Request` | Once per registered ID for each request handled by `di.Middleware` | Middleware starts cleanup on request cancellation or handler return |

For type registration, place a scope tag on any field. The field's value is not
used or populated by the container:

```go
type Job struct {
	_ di.Scope `di.scope:"prototype"`
}
```

A factory is useful when construction needs arguments or can fail:

```go
func registerMessage() error {
	_, err := di.RegisterBeanFactory("message", di.Prototype, func(context.Context) (interface{}, error) {
		value := "Hello from a factory"
		return &value, nil
	})
	return err
}
```

Import `context` for this snippet. Factories can use `di.GetInstanceSafe` to look
up other beans. Request beans and the beans they inject receive a context
derived from the HTTP request, canceled when the request ends; standalone
singleton and prototype resolutions receive `context.Background()`. If a factory fails, it
must release resources it created but did not return successfully, including any value
returned alongside a non-nil error.

## Field injection

Use `di.inject` on pointer or interface fields, including unexported fields.
An explicit ID selects one registration; an empty tag selects the unique
registered type assignable to the field:

```go
type Service struct {
	Config  *Config           `di.inject:"config"`
	Logger  Logger            `di.inject:""`
	Metrics *Metrics          `di.inject:"metrics" di.optional:"true"`
	Plugins []Plugin          `di.inject:""`
	ByID    map[string]Plugin `di.inject:""`
}
```

Here `Logger` and `Plugin` are application-defined interfaces and `Metrics` is
an application-defined struct. The rules are:

- Pointer and interface fields require one matching bean. Missing or ambiguous
  matches return an initialization error.
- `di.optional:"true"` permits a missing bean and leaves the field unchanged
  (normally nil). It does not suppress ambiguity, type errors, or construction
  and initialization errors.
- Slices and maps collect all assignable registered types. Elements must be
  pointers or interfaces; map keys must have string kind. Slices are ordered by
  bean ID, and map keys are bean IDs. A collection with no candidates is empty;
  an optional collection with no candidates remains unchanged.
- Collection matching uses the element type, regardless of the `di.inject` tag's
  value. Use an empty tag to express this clearly.
- Factory result types are unknown at registration, so factories require an
  explicit ID for injection and are excluded from automatic type matching.
- Request beans cannot be injected into other beans or retrieved with
  `GetInstance` or `GetInstanceSafe`; use the HTTP request context instead.

Supplied instances and factory results do **not** receive field injection. Set
up their fields yourself before returning or registering them.

## Initialization and lifecycle hooks

The container wires the dependency graph, initializes field dependencies, and
then invokes each bean's callbacks in this order:

1. `SetContext(context.Context)`, if it implements `di.ContextAwareBean`.
2. `PostConstruct() error`, if it implements `di.InitializingBean`.
3. Registered postprocessors, in registration order, for the bean's exact runtime type.

An error stops initialization. For example, a bean can validate its injected
configuration before application work begins:

```go
func (g *Greeter) PostConstruct() error {
	if g.Config.Greeting == "" {
		return fmt.Errorf("greeting must not be empty")
	}
	return nil
}
```

To initialize a type you cannot modify, register a postprocessor before startup:

```go
func registerConfigDefaults() error {
	return di.RegisterBeanPostprocessor(reflect.TypeOf((*Config)(nil)), func(instance interface{}) error {
		config := instance.(*Config)
		if config.Greeting == "" {
			config.Greeting = "Hello"
		}
		return nil
	})
}
```

Independent startup roots and type-matching candidates are visited in sorted
bean-ID order. Dependencies take precedence over that order. Field injection
waits for an unrelated dependency's initialization to finish, including when a
startup callback launched its initialization in another goroutine. Startup also
waits for admitted lookups and checks every singleton's final result before
returning success.

### Cycles and callback lookups

Singleton field-injection cycles are supported: references are wired before
hooks run, but cycle members cannot assume each other's hooks have completed.
Tagged cycles requiring repeated creation of a prototype return an error.

Factories and hooks can look up beans during startup, including retrieving a
singleton from its own hook. These manual lookups may expose an in-progress
singleton. A lookup that re-enters unfinished singleton construction returns an
error. Cycles formed by manual lookups inside callbacks are **not detected** and
must be avoided. Application goroutines must wait for successful startup.

### Failure and cleanup

If startup fails, the container closes beans it created during that attempt and
retains registrations for retry. Supplied instances are retained; their hooks
can run again on retry. Failed prototype or request resolutions close newly
created beans in their dependency graph. Failed request contexts are canceled
before cleanup, so closers can wait for context-bound work to stop.

Cleanup invokes `io.Closer.Close` where implemented. Returned cleanup errors are
logged without replacing the initialization error. Panics from factories and
callbacks propagate; `GetInstanceSafe` returns ordinary lookup errors but does
not recover callback panics. `GetInstance` panics on lookup errors as well.

After successful startup, prototypes remain the caller's or consuming bean's
responsibility, including prototypes injected into singletons or request beans.
Factory results that compare equal to a supplied singleton share its cleanup
ownership, including zero-sized values; shutdown closes that shared instance once.

### Shutdown

Stop application work before calling `di.Close()`. It waits for startup or
rollback, rejects new lookups once shutdown begins, waits for active lookups,
and closes singletons in reverse initialization order. Concurrent callers wait
for the same shutdown and container reset. All registrations and postprocessors
are cleared afterward. Supplied instances can also be closed before startup.

Returned beans can outlive the lookup that created them. Drain handlers and
background workers, and coordinate outstanding asynchronous request cleanup,
before closing shared singleton resources.

Do not call or wait for `di.Close()` inside a factory, context setter,
initialization hook, postprocessor, or bean closer: shutdown would be waiting
for that callback to finish. `GetBeanTypes()` and `GetBeanScopes()` are safe to
call from callbacks and return copies of the registration maps. Factory
registrations appear only in `GetBeanScopes()`.

## HTTP request scope

Wrap a standard `http.Handler` with `di.Middleware`. It creates every request
bean in sorted bean-ID order and puts it in the request context under
`di.BeanKey(beanID)`. This example measures elapsed time from bean initialization:

```go
package requestexample

import (
	"fmt"
	"log"
	"net/http"
	"reflect"
	"time"

	"github.com/goioc/di"
)

type RequestInfo struct {
	_       di.Scope `di.scope:"request"`
	Started time.Time
}

func (info *RequestInfo) PostConstruct() error {
	info.Started = time.Now()
	return nil
}

func Handler() (http.Handler, error) {
	if _, err := di.RegisterBean("requestInfo", reflect.TypeOf((*RequestInfo)(nil))); err != nil {
		return nil, err
	}
	if err := di.InitializeContainer(); err != nil {
		return nil, err
	}
	return di.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		info := r.Context().Value(di.BeanKey("requestInfo")).(*RequestInfo)
		if _, err := fmt.Fprintf(w, "Request elapsed: %s\n", time.Since(info.Started)); err != nil {
			log.Printf("write response: %v", err)
		}
	})), nil
}
```

Call `Handler` once during application startup, after registering any shared
beans. The application owns the HTTP server's shutdown and the final `di.Close()`.

Successful request beans implementing `io.Closer` are closed asynchronously
when the request is canceled or the wrapped handler returns. Middleware does
not wait for those closers or cancel the caller's original request context.
Request construction and initialization errors panic in the handler goroutine;
place recovery middleware outside `di.Middleware` if you want to handle them.

## Development

```sh
go test -race -count=2 ./...
go vet ./...
```

See the [tests](di_test.go) for more injection examples and the
[API reference](https://pkg.go.dev/github.com/goioc/di) for function documentation.
Licensed under the [MIT License](LICENSE).
