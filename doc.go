// Package di provides a process-wide dependency injection container with
// singleton, prototype, and HTTP request scopes.
//
// Register beans by type, instance, or factory before calling InitializeContainer.
// Type-registered beans receive field injection through di.inject tags. Supplied
// instances and factory results receive lifecycle callbacks but no field injection.
// Wait for initialization to succeed before serving requests or sharing beans
// with application goroutines. Use Middleware to obtain request-scoped beans
// from an HTTP request's context, and Close to release singletons at shutdown.
package di
