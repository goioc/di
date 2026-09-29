package di

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func containerForTest() *container {
	initializeShutdownLock.Lock()
	defer initializeShutdownLock.Unlock()
	return newContainer()
}

func TestInitializationRequiresCompletedWiring(t *testing.T) {
	defer resetContainer()
	initialized := 0
	_, err := RegisterBeanInstance("bean", &callbackBean{initHook: func() error { initialized++; return nil }})
	require.NoError(t, err)
	c := containerForTest()
	// An early initialization attempt must not invoke hooks or poison the node.
	require.EqualError(t, c.initialize(c.singletons["bean"], make(map[*beanNode]bool)), "bean initialization is already in progress: bean")
	require.Zero(t, initialized)
	_, err = c.resolve(context.Background(), "bean")
	require.NoError(t, err)
	require.Equal(t, 1, initialized)
}

type dependencyFailureConsumer struct {
	Dependency  *callbackBean `di.inject:"dependency"`
	initialized bool
}

func (b *dependencyFailureConsumer) PostConstruct() error { b.initialized = true; return nil }

func TestSharedDependencyFailureStopsBothConsumers(t *testing.T) {
	defer resetContainer()
	for _, id := range []string{"a", "b"} {
		_, err := RegisterBean(id, reflect.TypeOf((*dependencyFailureConsumer)(nil)))
		require.NoError(t, err)
	}
	failure := errors.New("shared dependency failed")
	initialized, closed := 0, 0
	_, err := RegisterBeanFactory("dependency", Singleton, func(context.Context) (interface{}, error) {
		return &callbackBean{
			initHook:  func() error { initialized++; return failure },
			closeHook: func() error { closed++; return nil },
		}, nil
	})
	require.NoError(t, err)
	c := containerForTest()
	// Stage two resolutions that both finish wiring before the shared hook fails.
	// This exercises the interleaving without relying on goroutine scheduling.
	a, err := c.build(context.Background(), "a", &resolution{}, make(map[string]bool))
	require.NoError(t, err)
	b, err := c.build(context.Background(), "b", &resolution{}, make(map[string]bool))
	require.NoError(t, err)
	for _, node := range []*beanNode{a, b} {
		require.ErrorIs(t, c.initialize(node, make(map[*beanNode]bool)), failure)
		require.False(t, node.instance.(*dependencyFailureConsumer).initialized)
	}
	require.Equal(t, 1, initialized, "a failed dependency must not be initialized twice")
	c.rollback()
	require.Equal(t, 1, closed)
}

type mixedCleanupConsumer struct {
	Scope      Scope         `di.scope:"prototype"`
	Dependency *callbackBean `di.inject:"dependency"`
	closed     int
}

func (b *mixedCleanupConsumer) Close() error { b.closed++; return nil }

func TestResolutionCleanupLeavesSingletonDependenciesOpen(t *testing.T) {
	defer resetContainer()
	closed := 0
	_, err := RegisterBeanFactory("dependency", Singleton, func(context.Context) (interface{}, error) {
		return &callbackBean{closeHook: func() error { closed++; return nil }}, nil
	})
	require.NoError(t, err)
	_, err = RegisterBean("consumer", reflect.TypeOf((*mixedCleanupConsumer)(nil)))
	require.NoError(t, err)
	c := containerForTest()
	node, err := c.build(context.Background(), "consumer", &resolution{}, make(map[string]bool))
	require.NoError(t, err)
	require.NoError(t, c.initialize(node, make(map[*beanNode]bool)))
	require.NoError(t, c.finishInitialization())
	// Cleanup must respect ownership even when handed the entire mixed-scope graph.
	c.closeNodes([]*beanNode{node, c.singletons["dependency"]}, false, false)
	require.Equal(t, 1, node.instance.(*mixedCleanupConsumer).closed)
	require.Zero(t, closed)
	c.closeSingletons()
	require.Equal(t, 1, closed)
	require.Equal(t, 1, node.instance.(*mixedCleanupConsumer).closed)
}

func TestInjectorRejectsValueFieldsBeforeResolvingDependencies(t *testing.T) {
	instance := &struct {
		Value string `di.inject:""`
	}{Value: "unchanged"}
	// Registration also rejects this type. Check the injector's defensive
	// validation independently, before it can resolve or assign a dependency.
	c := &container{beans: map[string]reflect.Type{"bean": reflect.TypeOf(instance)}}
	resolved := false
	err := c.injectDependencies("bean", instance, func(string) (interface{}, error) {
		resolved = true
		return new(string), nil
	})
	require.EqualError(t, err, unsupportedDependencyType)
	require.False(t, resolved)
	require.Equal(t, "unchanged", instance.Value)
}

type parallelCycleOwner struct {
	Gate *callbackBean      `di.inject:"gate"`
	Peer *parallelCyclePeer `di.inject:"peer"`
}

type parallelCyclePeer struct {
	Owner *parallelCycleOwner `di.inject:"owner"`
}

func TestParallelSingletonFieldCycleDoesNotDeadlock(t *testing.T) {
	defer resetContainer()
	started, releaseHook := make(chan struct{}), make(chan struct{})
	_, err := RegisterBeanFactory("gate", Singleton, func(context.Context) (interface{}, error) {
		return &callbackBean{initHook: func() error { close(started); <-releaseHook; return nil }}, nil
	})
	require.NoError(t, err)
	_, err = RegisterBean("owner", reflect.TypeOf((*parallelCycleOwner)(nil)))
	require.NoError(t, err)
	_, err = RegisterBean("peer", reflect.TypeOf((*parallelCyclePeer)(nil)))
	require.NoError(t, err)
	c := containerForTest()
	// Wire the cycle once, then overlap initialization from its two entry points.
	owner, err := c.build(context.Background(), "owner", &resolution{}, make(map[string]bool))
	require.NoError(t, err)
	ownerDone := make(chan error, 1)
	go func() { ownerDone <- c.initialize(owner, make(map[*beanNode]bool)) }()
	<-started
	peerDone := make(chan error, 1)
	go func() { _, err := c.resolve(context.Background(), "peer"); peerDone <- err }()
	select {
	case err := <-peerDone:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Error("concurrent entry into a singleton field cycle deadlocked")
	}
	close(releaseHook)
	select {
	case err := <-ownerDone:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("owner initialization did not complete")
	}
	instance := owner.instance.(*parallelCycleOwner)
	require.Same(t, instance, instance.Peer.Owner)
}

func TestConsumerOutsideCycleWaitsForCycleInitialization(t *testing.T) {
	defer resetContainer()
	started, releaseHook := make(chan struct{}), make(chan struct{})
	_, err := RegisterBean("a", reflect.TypeOf((*singletonCycleA)(nil)))
	require.NoError(t, err)
	_, err = RegisterBean("b", reflect.TypeOf((*singletonCycleB)(nil)))
	require.NoError(t, err)
	_, err = RegisterBean("consumer", reflect.TypeOf((*cycleConsumer)(nil)))
	require.NoError(t, err)
	require.NoError(t, RegisterBeanPostprocessor(reflect.TypeOf((*singletonCycleA)(nil)), func(interface{}) error {
		close(started)
		<-releaseHook
		return nil
	}))
	c := containerForTest()
	cycleDone := make(chan error, 1)
	go func() { _, err := c.resolve(context.Background(), "a"); cycleDone <- err }()
	<-started
	consumerDone := make(chan error, 1)
	go func() { _, err := c.resolve(context.Background(), "consumer"); consumerDone <- err }()
	select {
	case <-consumerDone:
		t.Error("consumer treated an unrelated cycle as its own initialization path")
	case <-time.After(50 * time.Millisecond):
	}
	close(releaseHook)
	require.NoError(t, <-cycleDone)
	select {
	case err := <-consumerDone:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("cycle completion did not release its consumer")
	}
}

type cycleConsumer struct {
	Dependency *singletonCycleA `di.inject:"a"`
}

func TestStartupCommitRejectsIncompleteSingleton(t *testing.T) {
	defer resetContainer()
	_, err := RegisterBeanInstance("bean", new(string))
	require.NoError(t, err)
	c := containerForTest()
	// Committing an incomplete startup must keep rollback ownership intact.
	require.EqualError(t, c.finishInitialization(), "singleton initialization did not complete: bean")
	require.True(t, c.starting)
	require.Len(t, c.created, 1)
	_, err = c.resolve(context.Background(), "bean")
	require.NoError(t, err)
	require.NoError(t, c.finishInitialization())
	require.False(t, c.starting)
	require.Empty(t, c.created)
}
