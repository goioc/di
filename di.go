package di

import (
	"context"
	"errors"
	"reflect"
	"sort"
	"strconv"
	"sync"
	"unsafe"

	"github.com/sirupsen/logrus"
)

// Scope determines when a bean is created and who manages its lifetime.
type Scope string

const (
	// Singleton reuses one instance per registration after InitializeContainer.
	// Close releases singleton instances that implement io.Closer.
	Singleton Scope = "singleton"
	// Prototype creates an instance for each lookup or injection. After successful
	// initialization, the caller or consuming bean is responsible for its cleanup.
	Prototype Scope = "prototype"
	// Request creates an instance for each HTTP request handled by Middleware.
	// Middleware starts cleanup on cancellation or handler return. Request beans
	// cannot be field-injected or retrieved with GetInstance or GetInstanceSafe.
	Request Scope = "request"
)

type tag string

const (
	scope    tag = "di.scope"
	inject   tag = "di.inject"
	optional tag = "di.optional"
)

const (
	unsupportedDependencyType        = "unsupported dependency type: all injections must be done by pointer, interface, slice or map"
	beanAlreadyRegistered            = "bean with such ID is already registered, overwriting it"
	requestScopedBeansCantBeInjected = "request-scoped beans can't be injected: they can only be retrieved from the web-context"
)

var initializeShutdownLock sync.Mutex
var containerState lifecycleState
var runningContainer *container
var activeLookups int
var shutdownDone chan struct{}
var lifecycleChanged = sync.NewCond(&initializeShutdownLock)
var beans = make(map[string]reflect.Type)
var beanFactories = make(map[string]func(context.Context) (interface{}, error))
var scopes = make(map[string]Scope)
var singletonInstances = make(map[string]interface{})
var userCreatedInstances = make(map[string]bool)
var beanPostprocessors = make(map[reflect.Type][]func(bean interface{}) error)

// InitializingBean provides a callback after field injection and context setup.
type InitializingBean interface {
	// PostConstruct runs before registered postprocessors. Returning an error
	// fails the lookup or container startup. Hooks in a singleton field cycle
	// must not assume the other cycle members have finished initialization.
	PostConstruct() error
}

// ContextAwareBean receives a context before PostConstruct and postprocessors.
// Request beans and their prototype dependencies receive a context derived from
// the HTTP request; singleton dependencies and standalone prototype resolutions
// receive context.Background. Failed request initialization cancels its context
// before cleanup, and successful request contexts end when their parent is canceled.
type ContextAwareBean interface {
	// SetContext supplies the bean's context before its initialization callbacks.
	SetContext(ctx context.Context)
}

// logger is the package's own logger instance so that importing di never
// mutates the host application's global logrus configuration.
var logger = logrus.New()

// RegisterBeanPostprocessor appends a callback for an exact runtime bean type.
// Callbacks run in registration order after PostConstruct, including for supplied
// instances and factory results. An error stops further callbacks and fails
// initialization. Neither argument may be nil; registration must precede startup.
func RegisterBeanPostprocessor(beanType reflect.Type, postprocessor func(bean interface{}) error) error {
	initializeShutdownLock.Lock()
	defer initializeShutdownLock.Unlock()
	if containerState != uninitialized {
		return errors.New("container is already initialized: can't register bean postprocessor")
	}
	if beanType == nil {
		return errors.New("bean postprocessor type must not be nil")
	}
	if postprocessor == nil {
		return errors.New("bean postprocessor must not be nil")
	}
	beanPostprocessors[beanType] = append(beanPostprocessors[beanType], postprocessor)
	return nil
}

// InitializeContainer creates, wires, and initializes registered singletons.
// It waits for admitted lookups and verifies singleton initialization results
// before succeeding. Registration is frozen during startup and until Close.
//
// On failure, newly created resources are closed and registrations are retained
// for retry. Supplied instances are retained, so their callbacks may run again.
// Callback panics propagate after rollback. Factories and hooks may look up beans
// during startup; application work must wait for successful initialization.
func InitializeContainer() (err error) {
	initializeShutdownLock.Lock()
	if containerState != uninitialized {
		initializeShutdownLock.Unlock()
		return errors.New("container is already initialized: reinitialization is not supported")
	}
	c := newContainer()
	runningContainer = c
	containerState = initializing
	initializeShutdownLock.Unlock()

	succeeded := false
	defer func() {
		defer func() {
			initializeShutdownLock.Lock()
			if succeeded {
				containerState = initialized
			} else {
				runningContainer = nil
				containerState = uninitialized
			}
			lifecycleChanged.Broadcast()
			initializeShutdownLock.Unlock()
		}()
		if !succeeded {
			// Stop admitting lookups before rolling back this attempt. Callbacks
			// already resolving a bean retain the same container snapshot.
			initializeShutdownLock.Lock()
			containerState = rollingBack
			for activeLookups != 0 {
				lifecycleChanged.Wait()
			}
			initializeShutdownLock.Unlock()
			c.rollback()
		}
	}()
	for _, beanID := range c.ids(Singleton) {
		if _, err = c.resolve(context.Background(), beanID); err != nil {
			return err
		}
	}
	// Startup callbacks can launch parallel lookups. Wait for those lookups
	// before committing, so their singleton hooks and errors are accounted for.
	initializeShutdownLock.Lock()
	for activeLookups != 0 {
		lifecycleChanged.Wait()
	}
	err = c.finishInitialization()
	initializeShutdownLock.Unlock()
	if err != nil {
		return err
	}
	succeeded = true
	return nil
}

// RegisterBean registers a pointer-to-struct type for allocation and field injection.
// A di.scope tag selects its scope; without one it is Singleton. For example,
// reflect.TypeOf((*Service)(nil)) registers *Service. Fields tagged di.inject
// accept pointers, interfaces, slices, or maps with string keys.
//
// Registration must precede startup. A valid registration replaces any existing
// bean with the same ID and returns overwritten=true. Invalid input leaves the
// previous registration intact.
func RegisterBean(beanID string, beanType reflect.Type) (overwritten bool, err error) {
	initializeShutdownLock.Lock()
	defer initializeShutdownLock.Unlock()
	if containerState != uninitialized {
		return false, errors.New("container is already initialized: can't register new bean")
	}
	overwritten = isBeanRegistered(beanID)
	if beanType == nil || beanType.Kind() != reflect.Ptr {
		return false, errors.New("bean type must be a pointer")
	}
	if beanType.Elem().Kind() != reflect.Struct {
		return false, errors.New("bean type must be a pointer to a struct")
	}
	var existingBeanType reflect.Type
	if existingBeanType, _ = beans[beanID]; existingBeanType != nil {
		logger.WithFields(logrus.Fields{
			"id":              beanID,
			"registered bean": existingBeanType,
			"new bean":        beanType,
		}).Warn(beanAlreadyRegistered)
	}
	beanScope, err := getScope(beanType)
	if err != nil {
		return false, err
	}
	beanTypeElement := beanType.Elem()
	for i := 0; i < beanTypeElement.NumField(); i++ {
		field := beanTypeElement.Field(i)
		if _, ok := field.Tag.Lookup(string(inject)); !ok {
			continue
		}
		if field.Type.Kind() != reflect.Ptr && field.Type.Kind() != reflect.Interface &&
			field.Type.Kind() != reflect.Slice && field.Type.Kind() != reflect.Map {
			return false, errors.New(unsupportedDependencyType)
		}
		if field.Type.Kind() == reflect.Map && field.Type.Key().Kind() != reflect.String {
			return false, errors.New(unsupportedDependencyType)
		}
	}
	clearBeanRegistration(beanID)
	beans[beanID] = beanType
	scopes[beanID] = *beanScope
	return overwritten, nil
}

// RegisterBeanInstance registers an existing non-nil pointer as a Singleton.
// Its fields are not injected, but context setup, PostConstruct, and registered
// postprocessors still run during startup. Startup failure retains the instance;
// Close releases it if it implements io.Closer, even before initialization.
//
// Registration must precede startup. A valid registration replaces any existing
// bean with the same ID and returns overwritten=true. Invalid input leaves it intact.
func RegisterBeanInstance(beanID string, beanInstance interface{}) (overwritten bool, err error) {
	initializeShutdownLock.Lock()
	defer initializeShutdownLock.Unlock()
	if containerState != uninitialized {
		return false, errors.New("container is already initialized: can't register new bean")
	}
	overwritten = isBeanRegistered(beanID)
	beanType := reflect.TypeOf(beanInstance)
	if beanType == nil || beanType.Kind() != reflect.Ptr || reflect.ValueOf(beanInstance).IsNil() {
		return false, errors.New("bean instance must be a pointer")
	}
	var existingBeanType reflect.Type
	if existingBeanType, _ = beans[beanID]; existingBeanType != nil {
		logger.WithFields(logrus.Fields{
			"id":                beanID,
			"registered bean":   existingBeanType,
			"new bean instance": beanType,
		}).Warn(beanAlreadyRegistered)
	}
	clearBeanRegistration(beanID)
	beans[beanID] = beanType
	scopes[beanID] = Singleton
	singletonInstances[beanID] = beanInstance
	userCreatedInstances[beanID] = true
	return overwritten, nil
}

// RegisterBeanFactory registers a non-nil factory with Singleton, Prototype, or
// Request scope. The factory must return a non-nil pointer on success. Its result
// receives lifecycle callbacks but no field injection. Request factories receive
// a context derived from the HTTP request; other scopes receive context.Background.
// The factory must clean up resources it creates but does not return successfully.
//
// Factory registrations can be injected by ID, but are excluded from automatic
// type matching because their result types are unknown until construction.
// Registration must precede startup. A valid registration replaces any existing
// bean with the same ID and returns overwritten=true. Invalid input leaves it intact.
func RegisterBeanFactory(beanID string, beanScope Scope, beanFactory func(ctx context.Context) (interface{}, error)) (overwritten bool, err error) {
	initializeShutdownLock.Lock()
	defer initializeShutdownLock.Unlock()
	if containerState != uninitialized {
		return false, errors.New("container is already initialized: can't register new bean factory")
	}
	if beanScope != Singleton && beanScope != Prototype && beanScope != Request {
		return false, errors.New("unsupported scope: " + string(beanScope))
	}
	if beanFactory == nil {
		return false, errors.New("bean factory must not be nil")
	}
	overwritten = isBeanRegistered(beanID)
	var existingBeanType reflect.Type
	if existingBeanType, _ = beans[beanID]; existingBeanType != nil {
		logger.WithFields(logrus.Fields{
			"id":              beanID,
			"registered bean": existingBeanType,
		}).Warn(beanAlreadyRegistered)
	}
	clearBeanRegistration(beanID)
	scopes[beanID] = beanScope
	beanFactories[beanID] = beanFactory
	return overwritten, nil
}

// clearBeanRegistration removes all registration forms before replacing an ID.
// The caller must hold initializeShutdownLock.
func clearBeanRegistration(beanID string) {
	delete(beans, beanID)
	delete(beanFactories, beanID)
	delete(scopes, beanID)
	delete(singletonInstances, beanID)
	delete(userCreatedInstances, beanID)
}

func getScope(bean reflect.Type) (*Scope, error) {
	var beanScope string
	ok := false
	beanElement := bean.Elem()
	for i := 0; i < beanElement.NumField(); i++ {
		field := beanElement.Field(i)
		beanScope, ok = field.Tag.Lookup(string(scope))
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
	switch beanScope {
	case string(Singleton):
		return &singleton, nil
	case string(Prototype):
		return &prototype, nil
	case string(Request):
		return &request, nil
	}
	return nil, errors.New("unsupported scope: " + beanScope)
}

// injectDependencies fills tagged fields, including unexported ones, using the
// current resolution's resolver so construction tracks dependency ownership.
func (c *container) injectDependencies(beanID string, instance interface{}, resolve func(string) (interface{}, error)) error {
	logger.WithField("beanID", beanID).Trace("injecting dependencies")
	instanceElement := c.beans[beanID].Elem()
	for i := 0; i < instanceElement.NumField(); i++ {
		field := instanceElement.Field(i)
		dependency, ok := field.Tag.Lookup(string(inject))
		if !ok {
			continue
		}
		optionalDependency, err := isOptional(field)
		if err != nil {
			return err
		}
		target := reflect.ValueOf(instance).Elem().Field(i)
		target = reflect.NewAt(target.Type(), unsafe.Pointer(target.UnsafeAddr())).Elem()
		switch target.Kind() {
		case reflect.Ptr, reflect.Interface:
			err = c.injectField(beanID, dependency, target, optionalDependency, resolve)
		case reflect.Slice, reflect.Map:
			err = c.injectCollection(beanID, target, optionalDependency, resolve)
		default:
			err = errors.New(unsupportedDependencyType)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func (c *container) injectField(beanID, dependency string, target reflect.Value, optionalDependency bool, resolve func(string) (interface{}, error)) error {
	if dependency == "" {
		candidates := c.findInjectionCandidates(target.Type())
		if len(candidates) == 0 {
			if optionalDependency {
				return nil
			}
			return errors.New("no candidates found for the injection")
		}
		if len(candidates) > 1 {
			return errors.New("more than one candidate found for the injection")
		}
		dependency = candidates[0]
	}
	value, err := c.dependencyValue(beanID, dependency, optionalDependency, resolve)
	if err != nil || !value.IsValid() {
		return err
	}
	if !value.Type().AssignableTo(target.Type()) {
		return errors.New("bean is not assignable to dependency field")
	}
	target.Set(value)
	return nil
}

func (c *container) injectCollection(beanID string, target reflect.Value, optionalDependency bool, resolve func(string) (interface{}, error)) error {
	elementType := target.Type().Elem()
	if elementType.Kind() != reflect.Ptr && elementType.Kind() != reflect.Interface {
		return errors.New(unsupportedDependencyType)
	}
	candidates := c.findInjectionCandidates(elementType)
	if len(candidates) == 0 && optionalDependency {
		return nil
	}
	isMap := target.Kind() == reflect.Map
	if isMap {
		target.Set(reflect.MakeMap(target.Type()))
	} else {
		target.Set(reflect.MakeSlice(target.Type(), len(candidates), len(candidates)))
	}
	for i, dependency := range candidates {
		value, err := c.dependencyValue(beanID, dependency, false, resolve)
		if err != nil {
			return err
		}
		if isMap {
			key := reflect.ValueOf(dependency).Convert(target.Type().Key())
			target.SetMapIndex(key, value)
		} else {
			target.Index(i).Set(value)
		}
	}
	return nil
}

func (c *container) dependencyValue(beanID, dependency string, optionalDependency bool, resolve func(string) (interface{}, error)) (reflect.Value, error) {
	beanScope, found := c.scopes[dependency]
	if !found {
		if optionalDependency {
			return reflect.Value{}, nil
		}
		return reflect.Value{}, errors.New("no dependency found")
	}
	if beanScope == Request {
		return reflect.Value{}, errors.New(requestScopedBeansCantBeInjected)
	}
	logInjection(beanID, c.beans[beanID].Elem(), dependency, c.beans[dependency])
	instance, err := resolve(dependency)
	if err != nil {
		return reflect.Value{}, err
	}
	return reflect.ValueOf(instance), nil
}

func logInjection(beanID string, instanceElement reflect.Type, beanToInject string, beanToInjectType reflect.Type) {
	logger.WithFields(logrus.Fields{
		"bean":               beanID,
		"beanType":           instanceElement,
		"dependencyBean":     beanToInject,
		"dependencyBeanType": beanToInjectType,
	}).Trace("processing dependency")
}

func isOptional(field reflect.StructField) (bool, error) {
	optionalTag := field.Tag.Get(string(optional))
	value, err := strconv.ParseBool(optionalTag)
	if optionalTag != "" && err != nil {
		return false, errors.New("invalid di.optional value: " + optionalTag)
	}
	return value, nil
}

// findInjectionCandidates matches assignable registered types in bean-ID order.
// Factories are absent from c.beans because their result types are not declared.
func (c *container) findInjectionCandidates(fieldToInjectType reflect.Type) []string {
	var candidates []string
	for beanID, beanType := range c.beans {
		if beanType.AssignableTo(fieldToInjectType) {
			candidates = append(candidates, beanID)
		}
	}
	sort.Strings(candidates)
	return candidates
}

func validateFactoryInstance(beanInstance interface{}) error {
	beanType := reflect.TypeOf(beanInstance)
	if beanType == nil || beanType.Kind() == reflect.Ptr && reflect.ValueOf(beanInstance).IsNil() {
		return errors.New("bean factory must return a non-nil pointer")
	}
	if beanType.Kind() != reflect.Ptr {
		return errors.New("bean factory must return pointer")
	}
	return nil
}

func (c *container) initializeInstance(ctx context.Context, beanID string, instance interface{}) error {
	if impl, ok := instance.(ContextAwareBean); ok {
		impl.SetContext(ctx)
	}
	if impl, ok := instance.(InitializingBean); ok {
		logger.WithField("beanID", beanID).Trace("initializing bean")
		if err := impl.PostConstruct(); err != nil {
			return err
		}
	}
	for _, postprocessor := range c.postprocessors[reflect.TypeOf(instance)] {
		if err := postprocessor(instance); err != nil {
			return err
		}
	}
	return nil
}

// GetInstance returns a singleton or a new prototype by ID, panicking on lookup
// errors. Use GetInstanceSafe to receive those errors as return values instead.
func GetInstance(beanID string) interface{} {
	beanInstance, err := GetInstanceSafe(beanID)
	if err != nil {
		panic(err)
	}
	return beanInstance
}

// GetInstanceSafe returns a singleton or creates and initializes a prototype by ID.
// It returns errors for missing beans, request-scoped beans, failed construction
// or initialization, and lookups outside startup or an initialized container.
// It does not recover panics from factories or lifecycle callbacks.
//
// Callbacks may use it during startup, including singleton self-lookups. Such
// lookups may expose an in-progress singleton; wait for InitializeContainer to
// succeed before using beans from application goroutines.
func GetInstanceSafe(beanID string) (interface{}, error) {
	c, release, err := acquireContainer()
	if err != nil {
		return nil, err
	}
	defer release()
	if c.scopes[beanID] == Request {
		return nil, errors.New("request-scoped beans can't be retrieved directly from the container: they can only be retrieved from the web-context")
	}
	return c.resolve(context.Background(), beanID)
}

// getRequestBeanInstance resolves with an explicit context and panics on errors.
// The caller controls the parent context's lifetime and successful bean cleanup.
func getRequestBeanInstance(ctx context.Context, beanID string) interface{} {
	c, release, err := acquireContainer()
	if err != nil {
		panic(err)
	}
	defer release()
	instance, err := c.resolve(ctx, beanID)
	if err != nil {
		panic(err)
	}
	return instance
}

func isBeanRegistered(beanID string) bool {
	if _, ok := beans[beanID]; ok {
		return true
	}
	if _, ok := beanFactories[beanID]; ok {
		return true
	}
	return false
}

// GetBeanTypes returns a copy of types registered by RegisterBean or
// RegisterBeanInstance. Factory registrations are omitted. It is safe to call
// from lifecycle callbacks, including during startup, rollback, and shutdown.
func GetBeanTypes() map[string]reflect.Type {
	initializeShutdownLock.Lock()
	defer initializeShutdownLock.Unlock()
	beanTypes := make(map[string]reflect.Type)
	for k, v := range beans {
		beanTypes[k] = v
	}
	return beanTypes
}

// GetBeanScopes returns a copy of all registered scopes, including factories.
// It is safe to call from lifecycle callbacks during startup, rollback, and shutdown.
func GetBeanScopes() map[string]Scope {
	initializeShutdownLock.Lock()
	defer initializeShutdownLock.Unlock()
	beanScopes := make(map[string]Scope)
	for k, v := range scopes {
		beanScopes[k] = v
	}
	return beanScopes
}

// Close stops new lookups, waits for active lookups, and closes singletons in
// reverse initialization order. Close errors are logged and cleanup continues.
// Concurrent callers wait for the same shutdown and container reset to finish.
// It also waits for startup or rollback in progress. All registrations and
// postprocessors are cleared, allowing a fresh registration and startup cycle.
// Supplied instances can be closed even if InitializeContainer was never called.
// Stop application work before calling Close: returned beans can outlive a lookup.
// Do not call Close from a factory, initialization callback, or bean closer, or
// wait for Close inside one: shutdown must wait for that callback to finish.
func Close() {
	initializeShutdownLock.Lock()
	for containerState == initializing || containerState == rollingBack {
		lifecycleChanged.Wait()
	}
	if containerState == closing {
		done := shutdownDone
		initializeShutdownLock.Unlock()
		<-done
		return
	}
	c := runningContainer
	if c == nil {
		c = newContainer()
	}
	containerState = closing
	done := make(chan struct{})
	shutdownDone = done
	for activeLookups != 0 {
		lifecycleChanged.Wait()
	}
	initializeShutdownLock.Unlock()
	defer func() {
		initializeShutdownLock.Lock()
		resetContainerWithoutLock()
		close(done)
		lifecycleChanged.Broadcast()
		initializeShutdownLock.Unlock()
	}()
	c.closeSingletons()
}

func resetContainer() {
	initializeShutdownLock.Lock()
	defer initializeShutdownLock.Unlock()
	resetContainerWithoutLock()
}

func resetContainerWithoutLock() {
	containerState = uninitialized
	runningContainer = nil
	beans = make(map[string]reflect.Type)
	beanFactories = make(map[string]func(context.Context) (interface{}, error))
	scopes = make(map[string]Scope)
	singletonInstances = make(map[string]interface{})
	userCreatedInstances = make(map[string]bool)
	beanPostprocessors = make(map[reflect.Type][]func(bean interface{}) error)
}
