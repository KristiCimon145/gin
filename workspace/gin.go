// Copyright 2014 Manu Martinez-Almeida. All rights reserved.
// Use of this source code is governed by a MIT style
// license that can be found in the LICENSE file.

package gin

import (
	"fmt"
	"html/template"
	"net"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/gin-gonic/gin/render"
)

const defaultMultipartMemory = 32 << 20 // 32 MiB

var (
	default404Body = []byte("404 page not found")
	default405Body = []byte("405 method not allowed")
	defaultAppEngine bool
)

// HandlerFunc defines the handler used by gin middleware as return value.
type HandlerFunc func(*Context)

// HandlersChain defines a HandlerFunc array.
type HandlersChain []HandlerFunc

// Last returns the last handler in the chain. ie. the last handler is the main own.
func (c HandlersChain) Last() HandlerFunc {
	if length := len(c); length > 0 {
		return c[length-1]
	}
	return nil
}

// RouteInfo represents a request route's specification which contains method and path and its handler.
type RouteInfo struct {
	Method      string
	Path        string
	Handler     string
	HandlerFunc HandlerFunc
}

// RoutesInfo defines a RouteInfo array.
type RoutesInfo []RouteInfo

// Engine is the framework's instance, it contains the muxer, middleware and configuration settings.
// Create an instance of Engine, by using New() or Default()
type Engine struct {
	RouterGroup

	// Enables automatic redirection if the current route can't be matched but a
	// handler for the path with (without) the trailing slash exists.
	// For example if /foo/ is requested but a route only exists for /foo, the
	// client is redirected to /foo with http status code 301 for GET requests
	// and 307 for all other request methods.
	RedirectTrailingSlash bool

	// If enabled, the router tries to fix the current request path, if no
	// handle is registered for it.
	// It helps to solve path problems like double slashes or missing leading/trailing slashes.
	// First superfluous path elements are removed. Afterwards the router does a case-insensitive
	// lookup of the cleaned path. If a handle can be found for this route, the router makes a redirection
	// to the corrected path with status code 301 for GET requests and 307 for all other request methods.
	// For example /FOO and /..//Foo could be redirected to /foo.
	// RedirectTrailingSlash is independent of this option.
	RedirectFixedPath bool

	// If enabled, the router checks if another method is allowed for the current route,
	// if the current request can not be routed.
	// If this is the case, the request is answered with 'Method Not Allowed' and HTTP status code 405.
	// If no other method is allowed, the request is delegated to the NotFound handler.
	HandleMethodNotAllowed bool

	// If enabled, client IP will be parsed from the headers' keys matching the RemoteIPHeaders.
	// If no IP can be retrieved, it will fallback to the IP the request came from (ClientIP).
	ForwardedByClientIP bool

	// List of headers used to obtain the client IP.
	// Default: []string{"X-Forwarded-For", "X-Real-IP"}
	RemoteIPHeaders []string

	// TrustedPlatform if set to a constant value, trust the header set by that platform.
	// Default: PlatformGoogleAppEngine
	TrustedPlatform string

	// If enabled, the url.RawPath will be used to find parameters.
	UseRawPath bool

	// If enabled, the path will be unescaped.
	// If UseRawPath is false (by default), the Router will request url.Path, which is already unescaped.
	// If UseRawPath is true, the Router will use url.RawPath and unescape it.
	UnescapePathValues bool

	// If enabled, the path will be cleaned by removing extra slashes.
	// For example /foo//bar will be cleaned to /foo/bar.
	RemoveExtraSlash bool

	// Value of 'maxMemory' param that is given to http.Request's ParseMultipartForm
	// method call.
	MaxMultipartMemory int64

	// RemoveExtraSlash a parameter can be passed to the RedirectFixedPath.
	// If RedirectFixedPath is true, the path will be cleaned by removing extra slashes.
	// For example /foo//bar will be cleaned to /foo/bar.
	// RemoveExtraSlash is independent of this option.
	// Deprecated: Use RemoveExtraSlash instead.
	// TODO: remove this field in a future version.
	// RemoveExtraSlash bool

	ReadTimeout       time.Duration
	ReadHeaderTimeout time.Duration
	WriteTimeout      time.Duration
	IdleTimeout       time.Duration

	delims           render.Delims
	secureJSONPrefix string
	HTMLTemplates    *template.Template
	FuncMap          template.FuncMap
	allNoRoute       HandlersChain
	allNoMethod      HandlersChain
	noRoute          HandlersChain
	noMethod         HandlersChain
	pool             sync.Pool
	trees            methodTrees
	maxParams        uint16
	maxSections      uint16
	trustedProxies   []string
	trustedCIDRs     []*net.IPNet
}

var _ http.Handler = (*Engine)(nil)

// New returns a new blank Engine instance without any middleware attached.
// By default the configuration is:
// - RedirectTrailingSlash:  true
// - RedirectFixedPath:      false
// - HandleMethodNotAllowed: false
// - ForwardedByClientIP:    true
// - UseRawPath:             false
// - UnescapePathValues:     true
func New() *Engine {
	debugPrintWARNINGNew()
	engine := &Engine{
		RouterGroup: RouterGroup{
			Handlers: nil,
			basePath: "/",
			root:     true,
		},
		FuncMap:                template.FuncMap{},
		RedirectTrailingSlash:  true,
		RedirectFixedPath:      false,
		HandleMethodNotAllowed: false,
		ForwardedByClientIP:    true,
		RemoteIPHeaders:        []string{"X-Forwarded-For", "X-Real-IP"},
		TrustedPlatform:        PlatformGoogleAppEngine,
		UseRawPath:             false,
		RemoveExtraSlash:       false,
		UnescapePathValues:     true,
		MaxMultipartMemory:     defaultMultipartMemory,
		trees:                  make(methodTrees, 0, 9),
		delims:                 render.Delims{Start: "{{", End: "}}"},
		secureJSONPrefix:       "while(1);",
		trustedProxies:         []string{"0.0.0.0/0", "::/0"},
		ReadTimeout:            30 * time.Second,
		ReadHeaderTimeout:      10 * time.Second,
		WriteTimeout:           30 * time.Second,
		IdleTimeout:            120 * time.Second,
	}
	engine.RouterGroup.engine = engine
	engine.pool.New = func() any {
		return engine.allocateContext(engine.maxParams)
	}
	return engine
}

// Default returns an Engine instance with the Logger and Recovery middleware already attached.
func Default() *Engine {
	debugPrintWARNINGDefault()
	engine := New()
	engine.Use(Logger(), Recovery())
	return engine
}

func (engine *Engine) allocateContext(maxParams uint16) *Context {
	v := make(Params, 0, maxParams)
	skippedNodes := make([]skippedNode, 0, engine.maxSections)
	return &Context{engine: engine, params: &v, skippedNodes: &skippedNodes}
}

// Delims sets template left and right delimiters and returns a Engine instance.
func (engine *Engine) Delims(left, right string) *Engine {
	engine.delims = render.Delims{Start: left, End: right}
	return engine
}

// SecureJsonPrefix sets the secureJSONPrefix string and returns a Engine instance.
func (engine *Engine) SecureJsonPrefix(prefix string) *Engine {
	engine.secureJSONPrefix = prefix
	return engine
}

// Handler returns the HTTP handler of the Engine.
func (engine *Engine) Handler() http.Handler {
	if !engine.UseRawPath && !engine.UnescapePathValues {
		return engine
	}
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		engine.ServeHTTP(w, req)
	})
}

func (engine *Engine) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	c := engine.pool.Get().(*Context)
	c.writermem.reset(w)
	c.Request = req
	c.reset()

	engine.handleHTTPRequest(c)

	engine.pool.Put(c)
}

// HandleContext re-enters a context that has been rewritten.
// This can be used to write a custom router or middleware.
// Note: before calling this, you should set the context's Request and Writer.
func (engine *Engine) HandleContext(c *Context) {
	c.reset()
	engine.handleHTTPRequest(c)
	engine.pool.Put(c)
}

// Run attaches the router to a http.Server and starts listening and serving HTTP requests.
// It is a shortcut for http.ListenAndServe(addr, router)
// Note: this method will block the calling goroutine indefinitely unless an error happens.
func (engine *Engine) Run(addr ...string) (err error) {
	address := resolveAddress(addr)
	debugPrint("Listening and serving HTTP on %s\n", address)
	server := &http.Server{
		Addr:              address,
		Handler:           engine.Handler(),
		ReadTimeout:       engine.ReadTimeout,
		ReadHeaderTimeout: engine.ReadHeaderTimeout,
		WriteTimeout:      engine.WriteTimeout,
		IdleTimeout:       engine.IdleTimeout,
	}
	err = server.ListenAndServe()
	return
}

// RunTLS attaches the router to a http.Server and starts listening and serving HTTPS (secure) requests.
// It is a shortcut for http.ListenAndServeTLS(addr, certFile, keyFile, router)
// Note: this method will block the calling goroutine indefinitely unless an error happens.
func (engine *Engine) RunTLS(addr, certFile, keyFile string) (err error) {
	debugPrint("Listening and serving HTTPS on %s\n", addr)
	server := &http.Server{
		Addr:              addr,
		Handler:           engine.Handler(),
		ReadTimeout:       engine.ReadTimeout,
		ReadHeaderTimeout: engine.ReadHeaderTimeout,
		WriteTimeout:      engine.WriteTimeout,
		IdleTimeout:       engine.IdleTimeout,
	}
	err = server.ListenAndServeTLS(certFile, keyFile)
	return
}

// RunUnix attaches the router to a http.Server and starts listening and serving HTTP requests through the specified unix socket (ie. a file).
// Note: this method will block the calling goroutine indefinitely unless an error happens.
func (engine *Engine) RunUnix(file string) (err error) {
	debugPrint("Listening and serving HTTP on unix:/%s\n", file)
	listener, err := net.Listen("unix", file)
	if err != nil {
		return
	}
	defer listener.Close()
	defer os.Remove(file)
	server := &http.Server{
		Handler:           engine.Handler(),
		ReadTimeout:       engine.ReadTimeout,
		ReadHeaderTimeout: engine.ReadHeaderTimeout,
		WriteTimeout:      engine.WriteTimeout,
		IdleTimeout:       engine.IdleTimeout,
	}
	err = server.Serve(listener)
	return
}

// RunFd attaches the router to a http.Server and starts listening and serving HTTP requests through the specified file descriptor.
// Note: this method will block the calling goroutine indefinitely unless an error happens.
func (engine *Engine) RunFd(fd int) (err error) {
	debugPrint("Listening and serving HTTP on fd@%d\n", fd)
	f := os.NewFile(uintptr(fd), fmt.Sprintf("fd@%d", fd))
	listener, err := net.FileListener(f)
	if err != nil {
		return
	}
	defer listener.Close()
	server := &http.Server{
		Handler:           engine.Handler(),
		ReadTimeout:       engine.ReadTimeout,
		ReadHeaderTimeout: engine.ReadHeaderTimeout,
		WriteTimeout:      engine.WriteTimeout,
		IdleTimeout:       engine.IdleTimeout,
	}
	err = server.Serve(listener)
	return
}

// Routes returns a slice of RouteInfo's details.
func (engine *Engine) Routes() RoutesInfo {
	routes := make(RoutesInfo, 0)
	for _, tree := range engine.trees {
		routes = iterateTemplates(routes, tree.method, tree.root)
	}
	return routes
}

func iterateTemplates(routes RoutesInfo, method string, root *node) RoutesInfo {
	path := make([]byte, 0, 1024)
	if root.handlers != nil {
		routes = append(routes, RouteInfo{
			Method:      method,
			Path:        root.fullPath,
			Handler:     nameOfFunction(root.handlers[len(root.handlers)-1]),
			HandlerFunc: root.handlers[len(root.handlers)-1],
		})
	}
	routes = iterateTemplatesRecursive(routes, method, root, path)
	return routes
}

func iterateTemplatesRecursive(routes RoutesInfo, method string, root *node, path []byte) RoutesInfo {
	for _, child := range root.children {
		if child.handlers != nil {
			routes = append(routes, RouteInfo{
				Method:      method,
				Path:        child.fullPath,
				Handler:     nameOfFunction(child.handlers[len(child.handlers)-1]),
				HandlerFunc: child.handlers[len(child.handlers)-1],
			})
		}
		routes = iterateTemplatesRecursive(routes, method, child, path)
	}
	return routes
}

// SetHTMLTemplate associate a template with Engine as HTMLTemplates.
func (engine *Engine) SetHTMLTemplate(templ *template.Template) {
	engine.HTMLTemplates = templ
}

// LoadHTMLFiles loads a set of HTML files and associates them with HTMLTemplates.
func (engine *Engine) LoadHTMLFiles(files ...string) {
	if IsDebugging() {
		engine.HTMLTemplates = template.Must(template.New("").Delims(engine.delims.Start, engine.delims.End).Funcs(engine.FuncMap).ParseFiles(files...))
		return
	}
	templ := template.Must(template.New("").Delims(engine.delims.Start, engine.delims.End).Funcs(engine.FuncMap).ParseFiles(files...))
	engine.SetHTMLTemplate(templ)
}

// LoadHTMLGlob loads HTML files identified by a glob pattern and associates them with HTMLTemplates.
func (engine *Engine) LoadHTMLGlob(pattern string) {
	left, right := engine.delims.Start, engine.delims.End
	if IsDebugging() {
		engine.HTMLTemplates = template.Must(template.New("").Delims(left, right).Funcs(engine.FuncMap).ParseGlob(pattern))
		return
	}
	templ := template.Must(template.New("").Delims(left, right).Funcs(engine.FuncMap).ParseGlob(pattern))
	engine.SetHTMLTemplate(templ)
}

// SetTrustedProxies set a list of network origins (IPv4 addresses,
// IPv4 CIDRs, IPv6 addresses or IPv6 CIDRs) from which to trust
// request's headers that contain alternative client IP when
// `Engine.ForwardedByClientIP` is `true`. By default, the list is
// `[]string{"0.0.0.0/0", "::/0"}` which means that all headers are trusted.
func (engine *Engine) SetTrustedProxies(proxies []string) error {
	engine.trustedProxies = proxies
	return engine.prepareTrustedCIDRs()
}

func (engine *Engine) isTrustedProxy(ip net.IP) bool {
	if engine.trustedCIDRs == nil {
		return false
	}
	for _, cidr := range engine.trustedCIDRs {
		if cidr.Contains(ip) {
			return true
		}
	}
	return false
}

func (engine *Engine) prepareTrustedCIDRs() error {
	if engine.trustedProxies == nil {
		return nil
	}

	cidr := make([]*net.IPNet, 0, len(engine.trustedProxies))
	for _, proxy := range engine.trustedProxies {
		if ip := net.ParseIP(proxy); ip != nil {
			ipnet := &net.IPNet{IP: ip, Mask: net.CIDRMask(len(ip)*8, len(ip)*8)}
			cidr = append(cidr, ipnet)
			continue
	}
		_, ipnet, err := net.ParseCIDR(proxy)
		if err != nil {
			return err
		}
		cidr = append(cidr, ipnet)
	}
	engine.trustedCIDRs = cidr
	return nil
}

func (engine *Engine) handleHTTPRequest(c *Context) {
	httpMethod := c.Request.Method
	rPath := c.Request.URL.Path
	unescape := false
	if engine.UseRawPath && len(c.Request.URL.RawPath) > 0 {
		rPath = c.Request.URL.RawPath
		unescape = engine.UnescapePathValues
	}

	if engine.RemoveExtraSlash {
		rPath = cleanPath(rPath)
	}

	// Find root of the tree for the given HTTP method
	t := engine.trees
	for i, tl := 0, len(t); i < tl; i++ {
		if t[i].method != httpMethod {
			continue
		}
		root := t[i].root
		// Find route in tree
		value := root.getValue(rPath, c.params, c.skippedNodes, unescape)
		if value.handlers != nil {
			c.handlers = value.handlers
			c.fullPath = value.fullPath
			c.Next()
			c.writermem.WriteHeaderNow()
			return
		}
		if httpMethod != http.MethodConnect && rPath != "/" {
			if value.tsr && engine.RedirectTrailingSlash {
				redirectTrailingSlash(c)
				return
			}
			if engine.RedirectFixedPath && redirectFixedPath(c, root, engine.RedirectFixedPath) {
				return
			}
		}
		break
	}

	if engine.HandleMethodNotAllowed {
		for _, tree := range engine.trees {
			if tree.method == httpMethod {
				continue
			}
			if value := tree.root.getValue(rPath, nil, c.skippedNodes, unescape); value.handlers != nil {
				c.handlers = engine.allNoMethod
				serveError(c, http.StatusMethodNotAllowed, default405Body)
				return
			}
		}
	}
	c.handlers = engine.allNoRoute
	serveError(c, http.StatusNotFound, default404Body)
}

func redirectTrailingSlash(c *Context) {
	req := c.Request
	p := req.URL.Path
	if req.URL.RawPath != "" {
		p = req.URL.RawPath
	}
	code := http.StatusMovedPermanently // 301
	if req.Method != http.MethodGet {
		code = http.StatusTemporaryRedirect // 307
	}

	if len(p) > 1 && p[len(p)-1] == '/' {
		req.URL.Path = p[:len(p)-1]
	} else {
		req.URL.Path = p + "/"
	}
	debugPrint("redirecting request %d: %s --> %s", code, p, req.URL.String())
	http.Redirect(c.Writer, req, req.URL.String(), code)
	c.writermem.WriteHeaderNow()
}

func redirectFixedPath(c *Context, root *node, trailingSlash bool) bool {
	req := c.Request
	rPath := req.URL.Path
	if req.URL.RawPath != "" {
		rPath = req.URL.RawPath
	}
	if fixedPath, ok := root.findCaseInsensitivePath(cleanPath(rPath), trailingSlash); ok {
		code := http.StatusMovedPermanently // 301
		if req.Method != http.MethodGet {
			code = http.StatusTemporaryRedirect // 307
		}
		req.URL.Path = string(fixedPath)
		debugPrint("redirecting request %d: %s --> %s", code, rPath, req.URL.String())
	http.Redirect(c.Writer, req, req.URL.String(), code)
	c.writermem.WriteHeaderNow()
		return true
	}
	return false
}

func serveError(c *Context, code int, defaultMessage []byte) {
	c.writermem.status = code
	c.Next()
	if c.writermem.Written() {
		return
	}
	if c.writermem.Status() == code {
		c.writermem.Header()["Content-Type"] = mimePlain
		_, err := c.Writer.Write(defaultMessage)
		if err != nil {
			debugPrint("cannot write message to writer during serve error: %v", err)
		}
		return
	}
	c.writermem.WriteHeaderNow()
}
