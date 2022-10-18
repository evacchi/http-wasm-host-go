package wasm

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/textproto"
	"net/url"
	"sort"
	"strings"

	"github.com/http-wasm/http-wasm-host-go/api/handler"
)

type host struct{}

var _ handler.Host = host{}

// EnableFeatures implements the same method as documented on handler.Host.
func (host) EnableFeatures(ctx context.Context, features handler.Features) handler.Features {
	if s, ok := ctx.Value(requestStateKey{}).(*requestState); ok {
		s.enableFeatures(features)
	}
	// Otherwise, this was called during init, but there's nothing to do
	// because net/http supports all features.
	return features
}

// GetMethod implements the same method as documented on handler.Host.
func (host) GetMethod(ctx context.Context) string {
	r := requestStateFromContext(ctx).r
	return r.Method
}

// SetMethod implements the same method as documented on handler.Host.
func (host) SetMethod(ctx context.Context, method string) {
	r := requestStateFromContext(ctx).r
	r.Method = method
}

// GetURI implements the same method as documented on handler.Host.
func (host) GetURI(ctx context.Context) string {
	r := requestStateFromContext(ctx).r
	u := r.URL
	result := u.EscapedPath()
	if result == "" {
		result = "/"
	}
	if u.ForceQuery || u.RawQuery != "" {
		result += "?" + u.RawQuery
	}
	return result
}

// SetURI implements the same method as documented on handler.Host.
func (host) SetURI(ctx context.Context, uri string) {
	r := requestStateFromContext(ctx).r
	u, err := url.ParseRequestURI(uri)
	if err != nil {
		panic(err)
	}
	r.URL.RawPath = u.RawPath
	r.URL.Path = u.Path
	r.URL.ForceQuery = u.ForceQuery
	r.URL.RawQuery = u.RawQuery
}

// GetProtocolVersion implements the same method as documented on handler.Host.
func (host) GetProtocolVersion(ctx context.Context) string {
	r := requestStateFromContext(ctx).r
	return r.Proto
}

// GetRequestHeaderNames implements the same method as documented on handler.Host.
func (host) GetRequestHeaderNames(ctx context.Context) (names []string) {
	r := requestStateFromContext(ctx).r
	count := len(r.Header)
	i := 0
	if r.Host != "" { // special-case the host header.
		count++
		names = make([]string, count)
		names[i] = "Host"
		i++
	} else {
		names = make([]string, count)
	}
	for n := range r.Header {
		if strings.HasPrefix(n, http.TrailerPrefix) {
			continue
		}
		names[i] = n
		i++
	}
	// Keys in a Go map don't have consistent ordering.
	sort.Strings(names)
	return
}

// GetRequestHeader implements the same method as documented on handler.Host.
func (host) GetRequestHeader(ctx context.Context, name string) (string, bool) {
	r := requestStateFromContext(ctx).r
	if textproto.CanonicalMIMEHeaderKey(name) == "Host" { // special-case the host header.
		v := r.Host
		return v, v != ""
	}
	if values := r.Header.Values(name); len(values) == 0 {
		return "", false
	} else {
		return values[0], true
	}
}

// SetRequestHeader implements the same method as documented on handler.Host.
func (host) SetRequestHeader(ctx context.Context, name, value string) {
	s := requestStateFromContext(ctx)
	s.r.Header.Set(name, value)
}

// RequestBodyReader implements the same method as documented on handler.Host.
func (host) RequestBodyReader(ctx context.Context) io.ReadCloser {
	s := requestStateFromContext(ctx)
	return s.r.Body
}

// RequestBodyWriter implements the same method as documented on handler.Host.
func (host) RequestBodyWriter(ctx context.Context) io.Writer {
	s := requestStateFromContext(ctx)
	var b bytes.Buffer // reset
	s.r.Body = io.NopCloser(&b)
	return &b
}

// GetRequestTrailerNames implements the same method as documented on handler.Host.
func (host) GetRequestTrailerNames(ctx context.Context) (names []string) {
	header := requestStateFromContext(ctx).w.Header()
	return trailerNames(header)
}

// GetRequestTrailer implements the same method as documented on handler.Host.
func (host) GetRequestTrailer(ctx context.Context, name string) (string, bool) {
	header := requestStateFromContext(ctx).w.Header()
	return getTrailer(header, name)
}

// SetRequestTrailer implements the same method as documented on handler.Host.
func (host) SetRequestTrailer(ctx context.Context, name, value string) {
	header := requestStateFromContext(ctx).w.Header()
	setTrailer(header, name, value)
}

// Next implements the same method as documented on handler.Host.
func (host) Next(ctx context.Context) {
	requestStateFromContext(ctx).handleNext()
}

// GetStatusCode implements the same method as documented on handler.Host.
func (host) GetStatusCode(ctx context.Context) uint32 {
	s := requestStateFromContext(ctx)
	if statusCode := s.w.(*bufferingResponseWriter).statusCode; statusCode == 0 {
		return 200 // default
	} else {
		return statusCode
	}
}

// SetStatusCode implements the same method as documented on handler.Host.
func (host) SetStatusCode(ctx context.Context, statusCode uint32) {
	s := requestStateFromContext(ctx)
	if w, ok := s.w.(*bufferingResponseWriter); ok {
		w.statusCode = statusCode
	} else {
		s.w.WriteHeader(int(statusCode))
	}
}

// GetResponseHeaderNames implements the same method as documented on handler.Host.
func (host) GetResponseHeaderNames(ctx context.Context) (names []string) {
	w := requestStateFromContext(ctx).w

	// allocate capacity == count though it might be smaller due to trailers.
	count := len(w.Header())
	names = make([]string, 0, count)

	for n := range w.Header() {
		if strings.HasPrefix(n, http.TrailerPrefix) {
			continue
		}
		names = append(names, n)
	}
	return
}

// GetResponseHeader implements the same method as documented on handler.Host.
func (host) GetResponseHeader(ctx context.Context, name string) (string, bool) {
	w := requestStateFromContext(ctx).w
	if values := w.Header().Values(name); len(values) == 0 {
		return "", false
	} else {
		return values[0], true
	}
}

// SetResponseHeader implements the same method as documented on handler.Host.
func (host) SetResponseHeader(ctx context.Context, name, value string) {
	s := requestStateFromContext(ctx)
	s.w.Header().Set(name, value)
}

// ResponseBodyReader implements the same method as documented on handler.Host.
func (host) ResponseBodyReader(ctx context.Context) io.ReadCloser {
	s := requestStateFromContext(ctx)
	body := s.w.(*bufferingResponseWriter).body
	return io.NopCloser(bytes.NewReader(body))
}

// ResponseBodyWriter implements the same method as documented on handler.Host.
func (host) ResponseBodyWriter(ctx context.Context) io.Writer {
	s := requestStateFromContext(ctx)
	if w, ok := s.w.(*bufferingResponseWriter); ok {
		w.body = nil // reset
		return w
	} else {
		return s.w
	}
}

// GetResponseTrailerNames implements the same method as documented on handler.Host.
func (host) GetResponseTrailerNames(ctx context.Context) (names []string) {
	header := requestStateFromContext(ctx).w.Header()
	return trailerNames(header)
}

// GetResponseTrailer implements the same method as documented on handler.Host.
func (host) GetResponseTrailer(ctx context.Context, name string) (string, bool) {
	header := requestStateFromContext(ctx).w.Header()
	return getTrailer(header, name)
}

// SetResponseTrailer implements the same method as documented on handler.Host.
func (host) SetResponseTrailer(ctx context.Context, name, value string) {
	header := requestStateFromContext(ctx).w.Header()
	setTrailer(header, name, value)
}

func trailerNames(header http.Header) (names []string) {
	// We don't pre-allocate as there may be no trailers.
	for n := range header {
		if strings.HasPrefix(n, http.TrailerPrefix) {
			n = n[len(http.TrailerPrefix):]
			names = append(names, n)
		}
	}
	return
}

func getTrailer(header http.Header, name string) (string, bool) {
	if values := header.Values(http.TrailerPrefix + name); len(values) == 0 {
		return "", false
	} else {
		return values[0], true
	}
}

func setTrailer(header http.Header, name string, value string) {
	header.Set(http.TrailerPrefix+name, value)
}
