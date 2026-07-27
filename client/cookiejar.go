// The code was originally taken from https://github.com/valyala/fasthttp/pull/526.
package client

import (
	"bytes"
	"net"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gofiber/utils/v2"
	utilsbytes "github.com/gofiber/utils/v2/bytes"
	utilsstrings "github.com/gofiber/utils/v2/strings"
	"github.com/valyala/fasthttp"
	"golang.org/x/net/publicsuffix"
)

const (
	maxCookieJarHosts = 1024

	// maxCookiesPerHost bounds the cookies stored under one storage key.
	// Scoping path-less cookies to their default-path (RFC 6265 Section
	// 5.1.4) means one origin can mint a distinct entry per directory it
	// serves, so the per-host count needs its own ceiling — the host cap
	// alone no longer bounds the jar. RFC 6265 Section 5.3 suggests evicting
	// when a domain exceeds a limit; 64 is comfortably above what real sites
	// use.
	maxCookiesPerHost = 64

	// defaultCookiePathStr is the path assumed for a cookie that carries no
	// Path attribute and for a request with no path (RFC 6265 Section 5.1.4).
	// Kept as a string so it cannot be mutated through a returned slice.
	defaultCookiePathStr = "/"
)

// defaultCookiePath returns the default path as bytes. It allocates nothing:
// the conversion of a constant string is resolved at compile time.
func defaultCookiePath() []byte { return []byte(defaultCookiePathStr) }

var cookieJarPool = sync.Pool{
	New: func() any {
		return &CookieJar{}
	},
}

// AcquireCookieJar returns an empty CookieJar object from the pool.
func AcquireCookieJar() *CookieJar {
	jar, ok := cookieJarPool.Get().(*CookieJar)
	if !ok {
		panic(errCookieJarTypeAssertion)
	}

	return jar
}

// ReleaseCookieJar returns a CookieJar object to the pool.
func ReleaseCookieJar(c *CookieJar) {
	c.Release()
	cookieJarPool.Put(c)
}

// CookieJar manages cookie storage for the client.
// CookieJar is safe for concurrent use, except Release. Release must not run
// concurrently with other methods, and the jar must not be used after Release.
type CookieJar struct {
	// hostCookies stores wrapped cookies keyed by storage scope:
	// host-only cookies use the request host, while domain cookies use the
	// accepted Domain attribute.
	// If release logic is re-enabled for these entries, iterate as storedCookie
	// values and call fasthttp.ReleaseCookie(stored.cookie) on the wrapped cookie.
	hostCookies map[string][]storedCookie
	mu          sync.Mutex
}

type storedCookie struct {
	cookie     *fasthttp.Cookie
	isHostOnly bool
}

type cookieDomainAcceptance struct {
	domain     string
	isHostOnly bool
	isOk       bool
}

// Get returns all cookies stored for a given URI. If there are no cookies for the
// provided host, the returned slice will be nil.
//
// The CookieJar keeps its own copies of cookies, so it is safe to release the returned
// cookies after use.
func (cj *CookieJar) Get(uri *fasthttp.URI) []*fasthttp.Cookie {
	if uri == nil {
		return nil
	}

	secure := bytes.Equal(uri.Scheme(), httpsScheme)
	return cj.getByHostAndPath(uri.Host(), uri.Path(), secure)
}

// getByHostAndPath returns cookies stored for a specific host and path.
func (cj *CookieJar) getByHostAndPath(host, path []byte, secure bool) []*fasthttp.Cookie {
	if cj.hostCookies == nil {
		return nil
	}

	var (
		err     error
		hostStr = utils.UnsafeString(host)
	)

	// port must not be included.
	hostStr, _, err = net.SplitHostPort(hostStr)
	if err != nil {
		hostStr = utils.UnsafeString(host)
	}
	return cj.cookiesForRequest(hostStr, path, secure)
}

// getCookiesByHost returns cookies stored for a specific host, removing any that have expired.
func (cj *CookieJar) getCookiesByHost(host string) []*fasthttp.Cookie {
	cj.mu.Lock()
	defer cj.mu.Unlock()

	now := time.Now()
	stored := cj.hostCookies[host]

	kept := stored[:0]
	for _, sc := range stored {
		c := sc.cookie
		// Remove expired cookies.
		if !c.Expire().Equal(fasthttp.CookieExpireUnlimited) && c.Expire().Before(now) {
			fasthttp.ReleaseCookie(c)
			continue
		}
		kept = append(kept, sc)
	}
	if len(kept) == 0 {
		delete(cj.hostCookies, host)
	} else {
		cj.hostCookies[host] = kept
	}

	out := make([]*fasthttp.Cookie, 0, len(kept))
	for _, sc := range kept {
		out = append(out, sc.cookie)
	}
	return out
}

// cookiesForRequest returns cookies that match the given host, path and security settings.
func (cj *CookieJar) cookiesForRequest(host string, path []byte, secure bool) []*fasthttp.Cookie { //nolint:revive // secure is a deliberate scheme filter, not a control-flow flag
	cj.mu.Lock()
	defer cj.mu.Unlock()

	host = utilsstrings.ToLower(host)
	now := time.Now()
	var matched []*fasthttp.Cookie

	for domain, cookies := range cj.hostCookies {
		if len(cookies) == 0 {
			continue
		}
		if !domainMatch(host, domain) {
			continue
		}

		kept := cookies[:0]
		for _, sc := range cookies {
			c := sc.cookie
			if !c.Expire().Equal(fasthttp.CookieExpireUnlimited) && c.Expire().Before(now) {
				fasthttp.ReleaseCookie(c)
				continue
			}
			kept = append(kept, sc)

			if sc.isHostOnly && host != domain {
				continue
			}
			if !pathMatch(path, c.Path()) {
				continue
			}
			if c.Secure() && !secure {
				continue
			}
			nc := fasthttp.AcquireCookie()
			nc.CopyTo(c)
			matched = append(matched, nc)
		}
		if len(kept) == 0 {
			delete(cj.hostCookies, domain)
		} else {
			cj.hostCookies[domain] = kept
		}
	}

	// RFC 6265 Section 5.4 step 2: cookies with a longer path sort first.
	// Map iteration order is random, so without this the winner among
	// same-named cookies at different paths differs run to run.
	// SliceStable keeps insertion order for equal paths, which stands in for
	// the RFC's creation-time tiebreak.
	sort.SliceStable(matched, func(i, j int) bool {
		return len(matched[i].Path()) > len(matched[j].Path())
	})

	return matched
}

// Set stores the given cookies for the specified URI host. If a cookie key already exists,
// it will be replaced by the new cookie value.
//
// CookieJar stores copies of the provided cookies, so they may be safely released after use.
func (cj *CookieJar) Set(uri *fasthttp.URI, cookies ...*fasthttp.Cookie) {
	if uri == nil {
		return
	}
	cj.SetByHost(uri.Host(), cookies...)
}

// SetByHost stores the given cookies for the specified host. If a cookie key already exists,
// it will be replaced by the new cookie value.
//
// CookieJar stores copies of the provided cookies, so they may be safely released after use.
func (cj *CookieJar) SetByHost(host []byte, cookies ...*fasthttp.Cookie) {
	hostStr := utils.UnsafeString(host)
	if h, _, err := net.SplitHostPort(hostStr); err == nil {
		hostStr = h
	}
	hostStr = utilsstrings.ToLower(hostStr)
	hostKey := utils.CopyString(hostStr)

	cj.mu.Lock()
	defer cj.mu.Unlock()

	if cj.hostCookies == nil {
		cj.hostCookies = make(map[string][]storedCookie)
	}

	for _, cookie := range cookies {
		// Fold with utilsstrings.ToLower rather than in place: the cookie
		// belongs to the caller, and the jar documents that it only stores
		// copies. ToLower returns its input unchanged when there is nothing to
		// fold, so `domain` may alias the caller's cookie buffer — every
		// retained use below copies (utils.CopyString for the map key,
		// Cookie.SetDomain for the stored value), and it must stay that way.
		domain := utilsstrings.ToLower(utils.UnsafeString(utils.TrimLeft(cookie.Domain(), '.')))
		key := hostKey
		storedDomain := hostStr
		isHostOnly := domain == ""
		if !isHostOnly {
			acceptance := acceptCookieDomain(hostStr, domain)
			if !acceptance.isOk {
				continue
			}
			isHostOnly = acceptance.isHostOnly
			if !isHostOnly {
				key = utils.CopyString(acceptance.domain)
				storedDomain = acceptance.domain
			}
		}

		cj.ensureHostCapacityLocked(key, time.Now())
		hostCookies := cj.hostCookies[key]

		existing := searchCookieByKeyAndPath(cookie.Key(), cookie.Path(), hostCookies)
		if existing == nil {
			existing = fasthttp.AcquireCookie()
			hostCookies = append(hostCookies, storedCookie{cookie: existing, isHostOnly: isHostOnly})
		} else {
			for i := range hostCookies {
				if hostCookies[i].cookie == existing {
					hostCookies[i].isHostOnly = isHostOnly
					break
				}
			}
		}
		existing.CopyTo(cookie)
		existing.SetDomain(storedDomain)
		cj.hostCookies[key] = hostCookies
		cj.enforceHostCookieLimitLocked(key, time.Now())
	}
}

// SetKeyValue sets a cookie for the specified host with the given key and value.
//
// This function helps prevent extra allocations by avoiding duplication of repeated cookies.
func (cj *CookieJar) SetKeyValue(host, key, value string) {
	c := fasthttp.AcquireCookie()
	defer fasthttp.ReleaseCookie(c)
	c.SetKey(key)
	c.SetValue(value)

	cj.SetByHost(utils.UnsafeBytes(host), c)
}

// SetKeyValueBytes sets a cookie for the specified host using byte slices for the key and value.
//
// This function helps prevent extra allocations by avoiding duplication of repeated cookies.
func (cj *CookieJar) SetKeyValueBytes(host string, key, value []byte) {
	c := fasthttp.AcquireCookie()
	defer fasthttp.ReleaseCookie(c)
	c.SetKeyBytes(key)
	c.SetValueBytes(value)

	cj.SetByHost(utils.UnsafeBytes(host), c)
}

// dumpCookiesToReq writes the stored cookies to the given request.
//
// cookiesForRequest returns them in RFC 6265 Section 5.4 order (longest path
// first). fasthttp keys request cookies by name and cannot represent the same
// name twice, so where several stored cookies share a name only the first —
// the most specific — is written; writing them all would let the least
// specific overwrite the most specific.
func (cj *CookieJar) dumpCookiesToReq(req *fasthttp.Request) {
	uri := req.URI()
	secure := bytes.Equal(uri.Scheme(), httpsScheme)
	cookies := cj.getByHostAndPath(uri.Host(), uri.Path(), secure)
	var seen map[string]struct{}
	if len(cookies) > 1 {
		seen = make(map[string]struct{}, len(cookies))
	}
	for _, cookie := range cookies {
		if seen != nil {
			name := utils.UnsafeString(cookie.Key())
			if _, dup := seen[name]; dup {
				fasthttp.ReleaseCookie(cookie)
				continue
			}
			seen[utils.CopyString(name)] = struct{}{}
		}
		req.Header.SetCookieBytesKV(cookie.Key(), cookie.Value())
		fasthttp.ReleaseCookie(cookie)
	}
}

// defaultCookiePathFor implements the RFC 6265 Section 5.1.4 default-path
// algorithm. A cookie that arrives without a Path attribute is scoped to the
// directory of the request that set it, not to the whole host: a Set-Cookie on
// "/a/b" defaults to "/a", so the cookie is not returned for "/".
func defaultCookiePathFor(requestPath []byte) []byte {
	if len(requestPath) == 0 || requestPath[0] != '/' {
		return defaultCookiePath()
	}
	i := bytes.LastIndexByte(requestPath, '/')
	if i <= 0 {
		return defaultCookiePath()
	}
	return requestPath[:i]
}

// setDefaultCookiePath stores path as c's Path attribute.
//
// fasthttp's Cookie.SetPathBytes runs the value through normalizePath, which
// percent-decodes it and turns ';' into a space. The path handed to us came
// from URI.Path(), which fasthttp has already decoded once, so setting it
// naively decodes twice: a request for "/a%2541b/c" would store the scope
// "/aAb" and the cookie could never be sent again, not even to the URL that
// set it. Escaping '%' survives the second decode; if the value still does not
// round-trip (a path containing ';', which normalizePath rewrites
// unconditionally), fall back to leaving the scope at "/" — broader than the
// RFC prescribes, but the cookie stays usable instead of being silently lost.
func setDefaultCookiePath(c *fasthttp.Cookie, path []byte) {
	c.SetPathBytes(escapePercent(path))
	if !bytes.Equal(c.Path(), path) {
		c.SetPathBytes(defaultCookiePath())
	}
}

// escapePercent returns p with every '%' rewritten as "%25", so one round of
// percent-decoding reproduces p exactly. It returns p unchanged when there is
// nothing to escape.
func escapePercent(p []byte) []byte {
	if bytes.IndexByte(p, '%') == -1 {
		return p
	}
	out := make([]byte, 0, len(p)+8)
	for _, b := range p {
		if b == '%' {
			out = append(out, '%', '2', '5')
			continue
		}
		out = append(out, b)
	}
	return out
}

// parseCookiesFromResp parses the cookies from the response and stores them for the specified host and path.
func (cj *CookieJar) parseCookiesFromResp(host, path []byte, resp *fasthttp.Response) {
	hostStr := utils.UnsafeString(host)
	if h, _, err := net.SplitHostPort(hostStr); err == nil {
		hostStr = h
	}
	hostStr = utilsstrings.ToLower(hostStr)
	hostKey := utils.CopyString(hostStr)

	cj.mu.Lock()
	defer cj.mu.Unlock()

	if cj.hostCookies == nil {
		cj.hostCookies = make(map[string][]storedCookie)
	}

	now := time.Now()
	defaultPath := defaultCookiePathFor(path)
	for _, value := range resp.Header.Cookies() {
		tmp := fasthttp.AcquireCookie()
		_ = tmp.ParseBytes(value) //nolint:errcheck // ignore error

		// A Set-Cookie without a Path attribute is scoped to the request's
		// directory, not to the whole host (RFC 6265 Section 5.1.4).
		if len(tmp.Path()) == 0 {
			setDefaultCookiePath(tmp, defaultPath)
		}

		domainBytes := utils.TrimLeft(tmp.Domain(), '.')
		utilsbytes.UnsafeToLower(domainBytes)
		key := hostKey
		isHostOnly := len(domainBytes) == 0
		if isHostOnly {
			tmp.SetDomain(hostStr)
		} else {
			domain := utils.UnsafeString(domainBytes)
			acceptance := acceptCookieDomain(hostStr, domain)
			if !acceptance.isOk {
				fasthttp.ReleaseCookie(tmp)
				continue
			}
			isHostOnly = acceptance.isHostOnly
			if isHostOnly {
				tmp.SetDomain(hostStr)
			} else {
				key = utils.CopyString(acceptance.domain)
				tmp.SetDomain(acceptance.domain)
			}
		}

		cj.ensureHostCapacityLocked(key, now)
		cookies := cj.hostCookies[key]
		c := searchCookieByKeyAndPath(tmp.Key(), tmp.Path(), cookies)
		if c == nil {
			c = fasthttp.AcquireCookie()
			cookies = append(cookies, storedCookie{cookie: c, isHostOnly: isHostOnly})
		} else {
			for i := range cookies {
				if cookies[i].cookie == c {
					cookies[i].isHostOnly = isHostOnly
					break
				}
			}
		}

		c.CopyTo(tmp)
		if c.Expire().Equal(fasthttp.CookieExpireUnlimited) || c.Expire().After(now) {
			cj.hostCookies[key] = cookies
			cj.enforceHostCookieLimitLocked(key, now)
		} else {
			kept := cookies[:0]
			for _, v := range cookies {
				if v.cookie != c {
					kept = append(kept, v)
				}
			}
			cj.hostCookies[key] = kept
			fasthttp.ReleaseCookie(c)
		}
		fasthttp.ReleaseCookie(tmp)
	}
}

// enforceHostCookieLimitLocked bounds the cookies stored under one key. It
// drops expired entries first and then the oldest remaining ones, which under
// insertion order are the least recently set.
func (cj *CookieJar) enforceHostCookieLimitLocked(key string, now time.Time) {
	cookies := cj.hostCookies[key]
	if len(cookies) <= maxCookiesPerHost {
		return
	}

	kept := cookies[:0]
	for _, sc := range cookies {
		if !sc.cookie.Expire().Equal(fasthttp.CookieExpireUnlimited) && sc.cookie.Expire().Before(now) {
			fasthttp.ReleaseCookie(sc.cookie)
			continue
		}
		kept = append(kept, sc)
	}

	if overflow := len(kept) - maxCookiesPerHost; overflow > 0 {
		for _, sc := range kept[:overflow] {
			fasthttp.ReleaseCookie(sc.cookie)
		}
		kept = append(kept[:0], kept[overflow:]...)
	}

	if len(kept) == 0 {
		delete(cj.hostCookies, key)
		return
	}
	cj.hostCookies[key] = kept
}

// ensureHostCapacityLocked bounds the number of stored hosts by evicting
// expired entries first and then one remaining host if the jar is still full.
func (cj *CookieJar) ensureHostCapacityLocked(key string, now time.Time) {
	if _, ok := cj.hostCookies[key]; ok || len(cj.hostCookies) < maxCookieJarHosts {
		return
	}

	for host, cookies := range cj.hostCookies {
		kept := cookies[:0]
		for _, sc := range cookies {
			if !sc.cookie.Expire().Equal(fasthttp.CookieExpireUnlimited) && sc.cookie.Expire().Before(now) {
				fasthttp.ReleaseCookie(sc.cookie)
				continue
			}
			kept = append(kept, sc)
		}
		if len(kept) == 0 {
			delete(cj.hostCookies, host)
			if len(cj.hostCookies) < maxCookieJarHosts {
				return
			}
			continue
		}
		cj.hostCookies[host] = kept
	}

	var evictHost string
	for host := range cj.hostCookies {
		if evictHost == "" || host < evictHost {
			evictHost = host
		}
	}
	if evictHost != "" {
		releaseStoredCookies(cj.hostCookies[evictHost])
		delete(cj.hostCookies, evictHost)
	}
}

// releaseStoredCookies releases pooled cookies for an evicted host entry.
func releaseStoredCookies(cookies []storedCookie) {
	for _, sc := range cookies {
		fasthttp.ReleaseCookie(sc.cookie)
	}
}

// Release releases all stored cookies. After this, the CookieJar is empty and
// must not be used again.
func (cj *CookieJar) Release() {
	// FOLLOW-UP performance optimization:
	// Currently, a race condition is found because the reset method modifies a value
	// that is not a copy but a reference. A solution would be to make a copy.
	// for _, v := range cj.hostCookies {
	//	  for _, c := range v {
	//		fasthttp.ReleaseCookie(c)
	//	  }
	// }
	cj.hostCookies = nil
}

// searchCookieByKeyAndPath looks up the stored cookie that a newly received
// cookie replaces. RFC 6265 Section 5.3 step 11 identifies a cookie by the
// triple (name, domain, path), and the caller has already selected the entry
// list for the domain — so the path must be *equal*, not merely path-matching.
// Using pathMatch here would let "a=2; Path=/admin" overwrite an existing
// "a=1; Path=/" instead of storing both.
func searchCookieByKeyAndPath(key, path []byte, cookies []storedCookie) *fasthttp.Cookie {
	for _, sc := range cookies {
		c := sc.cookie
		if bytes.Equal(key, c.Key()) && samePath(path, c.Path()) {
			return c
		}
	}
	return nil
}

// samePath compares two cookie paths, treating an empty path as the default
// "/" the same way pathMatch does.
func samePath(a, b []byte) bool {
	if len(a) == 0 {
		a = defaultCookiePath()
	}
	if len(b) == 0 {
		b = defaultCookiePath()
	}
	return bytes.Equal(a, b)
}

// pathMatch determines whether the request path matches the cookie path
// according to RFC 6265 section 5.1.4.
func pathMatch(reqPath, cookiePath []byte) bool {
	if len(reqPath) == 0 {
		reqPath = defaultCookiePath()
	}
	if len(cookiePath) == 0 {
		cookiePath = defaultCookiePath()
	}
	if bytes.Equal(reqPath, cookiePath) {
		return true
	}
	if !bytes.HasPrefix(reqPath, cookiePath) {
		return false
	}
	if cookiePath[len(cookiePath)-1] == '/' {
		return true
	}
	return len(reqPath) > len(cookiePath) && reqPath[len(cookiePath)] == '/'
}

// domainMatch reports whether host domain-matches the given cookie domain
// (RFC 6265 Section 5.1.3). The comparison itself is ASCII case-insensitive
// and allocation-free, but callers still normalize hosts and domains to
// lowercase: the jar's map keys and its exact-match checks (e.g. the
// host-only comparison in cookiesForRequest) rely on it.
func domainMatch(host, domain string) bool {
	if utils.EqualFold(host, domain) {
		return true
	}
	return len(host) > len(domain) &&
		host[len(host)-len(domain)-1] == '.' &&
		utils.HasSuffixFold(host, domain)
}

// acceptCookieDomain enforces RFC 6265 response-domain acceptance. Trailing-dot,
// exact-match public-suffix, and exact-match IP-literal Domain attributes are
// downgraded to host-only so same-host behavior is preserved without storing
// cookies under shared suffixes or allowing IP suffix matching across
// unrelated hosts.
func acceptCookieDomain(host, domain string) cookieDomainAcceptance {
	if strings.HasSuffix(domain, ".") {
		return cookieDomainAcceptance{domain: host, isHostOnly: true, isOk: true}
	}

	if host == domain {
		if isIPLiteral(domain) || isPublicSuffixDomain(domain) {
			return cookieDomainAcceptance{domain: host, isHostOnly: true, isOk: true}
		}
		return cookieDomainAcceptance{domain: domain, isOk: true}
	}

	if isIPLiteral(host) || isIPLiteral(domain) || isPublicSuffixDomain(domain) || !domainMatch(host, domain) {
		return cookieDomainAcceptance{}
	}

	return cookieDomainAcceptance{domain: domain, isOk: true}
}

func isIPLiteral(host string) bool {
	if len(host) >= 2 && host[0] == '[' && host[len(host)-1] == ']' {
		host = host[1 : len(host)-1]
	}

	// Equivalent to net.ParseIP(host) != nil: utils.ParseIPv4/ParseIPv6
	// accept the same strings once zoned addresses (which net.ParseIP
	// rejects) are screened out, without allocating on either outcome.
	if strings.IndexByte(host, '%') >= 0 {
		return false
	}
	if _, ok := utils.ParseIPv4(host); ok {
		return true
	}
	_, ok := utils.ParseIPv6(host)
	return ok
}

func isPublicSuffixDomain(domain string) bool {
	suffix, _ := publicsuffix.PublicSuffix(domain)

	return suffix == domain
}
