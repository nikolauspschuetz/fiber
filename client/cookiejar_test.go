package client

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"sort"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/valyala/fasthttp"
	"golang.org/x/net/publicsuffix"
)

func checkKeyValue(t *testing.T, cj *CookieJar, cookie *fasthttp.Cookie, uri *fasthttp.URI, n int) {
	t.Helper()

	cs := cj.Get(uri)
	require.GreaterOrEqual(t, len(cs), n)

	c := cs[n-1]
	require.NotNil(t, c)

	require.Equal(t, string(c.Key()), string(cookie.Key()))
	require.Equal(t, string(c.Value()), string(cookie.Value()))
}

func cookieKeys(cookies []*fasthttp.Cookie) []string {
	keys := make([]string, 0, len(cookies))
	for _, cookie := range cookies {
		keys = append(keys, string(cookie.Key()))
	}

	return keys
}

func Test_CookieJarGet(t *testing.T) {
	t.Parallel()

	url := []byte("http://fasthttp.com/")
	url1 := []byte("http://fasthttp.com/make/")
	url11 := []byte("http://fasthttp.com/hola")
	url2 := []byte("http://fasthttp.com/make/fasthttp")
	url3 := []byte("http://fasthttp.com/make/fasthttp/great")
	cj := &CookieJar{}

	c1 := &fasthttp.Cookie{}
	c1.SetKey("k")
	c1.SetValue("v")
	c1.SetPath("/make/")

	c2 := &fasthttp.Cookie{}
	c2.SetKey("kk")
	c2.SetValue("vv")
	c2.SetPath("/make/fasthttp")

	c3 := &fasthttp.Cookie{}
	c3.SetKey("kkk")
	c3.SetValue("vvv")
	c3.SetPath("/make/fasthttp/great")

	uri := fasthttp.AcquireURI()
	require.NoError(t, uri.Parse(nil, url))

	uri1 := fasthttp.AcquireURI()
	require.NoError(t, uri1.Parse(nil, url1))

	uri11 := fasthttp.AcquireURI()
	require.NoError(t, uri11.Parse(nil, url11))

	uri2 := fasthttp.AcquireURI()
	require.NoError(t, uri2.Parse(nil, url2))

	uri3 := fasthttp.AcquireURI()
	require.NoError(t, uri3.Parse(nil, url3))

	cj.Set(uri1, c1, c2, c3)

	cookies := cj.Get(uri1)
	require.Len(t, cookies, 1)
	for _, cookie := range cookies {
		require.True(t, bytes.HasPrefix(uri1.Path(), cookie.Path()))
	}

	cookies = cj.Get(uri11)
	require.Empty(t, cookies)

	cookies = cj.Get(uri2)
	require.Len(t, cookies, 2)
	for _, cookie := range cookies {
		require.True(t, bytes.HasPrefix(uri2.Path(), cookie.Path()))
	}

	cookies = cj.Get(uri3)
	require.Len(t, cookies, 3)
	for _, cookie := range cookies {
		require.True(t, bytes.HasPrefix(uri3.Path(), cookie.Path()))
	}

	cookies = cj.Get(uri)
	require.Empty(t, cookies)
}

func Test_CookieJarGetExpired(t *testing.T) {
	t.Parallel()

	url1 := []byte("http://fasthttp.com/make/")
	uri1 := fasthttp.AcquireURI()
	require.NoError(t, uri1.Parse(nil, url1))

	c1 := &fasthttp.Cookie{}
	c1.SetKey("k")
	c1.SetValue("v")
	c1.SetExpire(time.Now().Add(-time.Hour))

	cj := &CookieJar{}
	cj.Set(uri1, c1)

	cookies := cj.Get(uri1)
	require.Empty(t, cookies)
}

func Test_CookieJarSet(t *testing.T) {
	t.Parallel()

	url := []byte("http://fasthttp.com/hello/world")
	cj := &CookieJar{}

	cookie := &fasthttp.Cookie{}
	cookie.SetKey("k")
	cookie.SetValue("v")

	uri := fasthttp.AcquireURI()
	require.NoError(t, uri.Parse(nil, url))

	cj.Set(uri, cookie)
	checkKeyValue(t, cj, cookie, uri, 1)
}

func Test_CookieJarSetRepeatedCookieKeys(t *testing.T) {
	t.Parallel()
	host := "fast.http"
	cj := &CookieJar{}

	uri := fasthttp.AcquireURI()
	uri.SetHost(host)

	cookie := &fasthttp.Cookie{}
	cookie.SetKey("k")
	cookie.SetValue("v")

	cookie2 := &fasthttp.Cookie{}
	cookie2.SetKey("k")
	cookie2.SetValue("v2")

	cookie3 := &fasthttp.Cookie{}
	cookie3.SetKey("key")
	cookie3.SetValue("value")

	cj.Set(uri, cookie, cookie2, cookie3)

	cookies := cj.Get(uri)
	require.Len(t, cookies, 2)
	require.Equal(t, "k", string(cookies[0].Key()))
	require.Equal(t, "v2", string(cookies[0].Value()))
	require.Equal(t, host, string(cookies[0].Domain()))
	require.True(t, bytes.Equal(cookies[0].Value(), cookie2.Value()))
}

func Test_CookieJarSetKeyValue(t *testing.T) {
	t.Parallel()

	host := "fast.http"
	cj := &CookieJar{}

	uri := fasthttp.AcquireURI()
	uri.SetHost(host)

	cj.SetKeyValue(host, "k", "v")
	cj.SetKeyValue(host, "key", "value")
	cj.SetKeyValue(host, "k", "vv")
	cj.SetKeyValue(host, "key", "value2")
	cj.SetKeyValueBytes(host, []byte("kb"), []byte("vb"))

	cookies := cj.Get(uri)
	require.Len(t, cookies, 3)

	// Verify the entry written via SetKeyValueBytes has the exact key and value.
	var foundBytes bool
	for _, c := range cookies {
		if string(c.Key()) == "kb" {
			foundBytes = true
			require.Equal(t, "vb", string(c.Value()))
		}
	}
	require.True(t, foundBytes, "expected cookie kb=vb written by SetKeyValueBytes")
}

func Test_CookieJarHostStorageIsBounded(t *testing.T) {
	t.Parallel()

	cj := &CookieJar{}

	for i := range maxCookieJarHosts + 32 {
		host := fmt.Sprintf("host-%d.example.com", i)
		cookie := &fasthttp.Cookie{}
		cookie.SetKey("k")
		cookie.SetValue("v")
		cj.SetByHost([]byte(host), cookie)
	}

	require.LessOrEqual(t, len(cj.hostCookies), maxCookieJarHosts)
}

func Test_CookieJarHostEvictionIsDeterministic(t *testing.T) {
	t.Parallel()

	cj := &CookieJar{hostCookies: make(map[string][]storedCookie, maxCookieJarHosts)}
	for i := range maxCookieJarHosts {
		host := fmt.Sprintf("host-%04d.example.com", i+1)
		cookie := fasthttp.AcquireCookie()
		cookie.SetKey("k")
		cookie.SetValue("v")
		cj.hostCookies[host] = []storedCookie{{cookie: cookie, isHostOnly: true}}
	}

	cj.ensureHostCapacityLocked("zzz.example.com", time.Now())

	_, ok := cj.hostCookies["host-0001.example.com"]
	require.False(t, ok)
	require.Len(t, cj.hostCookies, maxCookieJarHosts-1)
}

func Test_CookieJarHostCapacityPrefersExpiredEntries(t *testing.T) {
	t.Parallel()

	cj := &CookieJar{hostCookies: make(map[string][]storedCookie, maxCookieJarHosts)}
	now := time.Now()

	expired := fasthttp.AcquireCookie()
	expired.SetKey("expired")
	expired.SetValue("v")
	expired.SetExpire(now.Add(-time.Minute))
	cj.hostCookies["expired.example.com"] = []storedCookie{{cookie: expired, isHostOnly: true}}

	for i := 1; i < maxCookieJarHosts; i++ {
		host := fmt.Sprintf("host-%04d.example.com", i)
		cookie := fasthttp.AcquireCookie()
		cookie.SetKey("k")
		cookie.SetValue("v")
		cj.hostCookies[host] = []storedCookie{{cookie: cookie, isHostOnly: true}}
	}

	cj.ensureHostCapacityLocked("new.example.com", now)

	_, ok := cj.hostCookies["expired.example.com"]
	require.False(t, ok)
	require.Len(t, cj.hostCookies, maxCookieJarHosts-1)
	_, ok = cj.hostCookies["host-0001.example.com"]
	require.True(t, ok)
}

func Test_CookieJarGetFromResponse(t *testing.T) {
	t.Parallel()

	res := fasthttp.AcquireResponse()
	host := []byte("fast.http")
	uri := fasthttp.AcquireURI()
	uri.SetHostBytes(host)

	c := &fasthttp.Cookie{}
	c.SetKey("key")
	c.SetValue("val")

	c2 := &fasthttp.Cookie{}
	c2.SetKey("k")
	c2.SetValue("v")

	c3 := &fasthttp.Cookie{}
	c3.SetKey("kk")
	c3.SetValue("vv")

	res.Header.SetStatusCode(200)
	res.Header.SetCookie(c)
	res.Header.SetCookie(c2)
	res.Header.SetCookie(c3)

	cj := &CookieJar{}
	cj.parseCookiesFromResp(host, nil, res)

	cookies := cj.Get(uri)
	require.Len(t, cookies, 3)
	values := map[string]string{"key": "val", "k": "v", "kk": "vv"}
	for _, c := range cookies {
		k := string(c.Key())
		v, ok := values[k]
		require.True(t, ok)
		require.Equal(t, v, string(c.Value()))
		delete(values, k)
	}
	require.Empty(t, values)
}

func Test_CookieJar_HostPort(t *testing.T) {
	t.Parallel()

	jar := &CookieJar{}
	uriSet := fasthttp.AcquireURI()
	require.NoError(t, uriSet.Parse(nil, []byte("http://fasthttp.com:80/path")))

	c := &fasthttp.Cookie{}
	c.SetKey("k")
	c.SetValue("v")
	jar.Set(uriSet, c)

	// retrieve using a different port to ensure port is ignored
	uriGet := fasthttp.AcquireURI()
	require.NoError(t, uriGet.Parse(nil, []byte("http://fasthttp.com:8080/path")))

	cookies := jar.Get(uriGet)
	require.Len(t, cookies, 1)
	require.Equal(t, "k", string(cookies[0].Key()))
	require.Equal(t, "v", string(cookies[0].Value()))
	require.Equal(t, "fasthttp.com", string(cookies[0].Domain()))
}

func Test_CookieJar_isIPLiteral(t *testing.T) {
	t.Parallel()

	// Pinned to net.ParseIP semantics: zoned addresses are rejected.
	tests := []struct {
		host string
		want bool
	}{
		{"192.0.2.1", true},
		{"::1", true},
		{"[::1]", true},
		{"::ffff:192.0.2.1", true},
		{"fe80::1%eth0", false},
		{"[fe80::1%eth0]", false},
		{"example.com", false},
		{"192.0.2.256", false},
		{"", false},
	}
	for _, tt := range tests {
		require.Equal(t, tt.want, isIPLiteral(tt.host), "isIPLiteral(%q)", tt.host)
	}
}

func Test_CookieJar_Domain(t *testing.T) {
	t.Parallel()

	jar := &CookieJar{}

	uri := fasthttp.AcquireURI()
	require.NoError(t, uri.Parse(nil, []byte("http://sub.example.com/")))

	c := &fasthttp.Cookie{}
	c.SetKey("k")
	c.SetValue("v")
	c.SetDomain("example.com")

	jar.Set(uri, c)

	uri2 := fasthttp.AcquireURI()
	require.NoError(t, uri2.Parse(nil, []byte("http://other.example.com/")))

	cookies := jar.Get(uri2)
	require.Len(t, cookies, 1)
	require.Equal(t, "k", string(cookies[0].Key()))
	require.Equal(t, "v", string(cookies[0].Value()))
}

func Test_CookieJar_HostOnlyCookieNotSentToSubdomain(t *testing.T) {
	t.Parallel()

	jar := &CookieJar{}
	origin := fasthttp.AcquireURI()
	require.NoError(t, origin.Parse(nil, []byte("http://example.com/")))

	c := &fasthttp.Cookie{}
	c.SetKey("sid")
	c.SetValue("123")
	jar.Set(origin, c)

	subdomain := fasthttp.AcquireURI()
	require.NoError(t, subdomain.Parse(nil, []byte("http://attacker.example.com/")))
	require.Empty(t, jar.Get(subdomain))
}

func Test_CookieJar_SetByHostDoesNotMutateHostOnlyCookieToDomainCookie(t *testing.T) {
	t.Parallel()

	jar := &CookieJar{}
	c := &fasthttp.Cookie{}
	c.SetKey("sid")
	c.SetValue("123")

	jar.SetByHost([]byte("example.com"), c)
	require.Empty(t, c.Domain())

	subOrigin := fasthttp.AcquireURI()
	defer fasthttp.ReleaseURI(subOrigin)
	require.NoError(t, subOrigin.Parse(nil, []byte("http://sub.example.com/")))
	jar.Set(subOrigin, c)
	require.Empty(t, c.Domain())

	sibling := fasthttp.AcquireURI()
	defer fasthttp.ReleaseURI(sibling)
	require.NoError(t, sibling.Parse(nil, []byte("http://other.example.com/")))
	require.Empty(t, jar.Get(sibling))
}

func Test_CookieJar_ResponseHostOnlyCookieNotSentToSubdomain(t *testing.T) {
	t.Parallel()

	jar := &CookieJar{}
	resp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseResponse(resp)

	c := &fasthttp.Cookie{}
	c.SetKey("sid")
	c.SetValue("123")
	resp.Header.SetCookie(c)

	jar.parseCookiesFromResp([]byte("example.com"), nil, resp)

	origin := fasthttp.AcquireURI()
	defer fasthttp.ReleaseURI(origin)
	require.NoError(t, origin.Parse(nil, []byte("http://example.com/")))
	require.Equal(t, []string{"sid"}, cookieKeys(jar.Get(origin)))

	subdomain := fasthttp.AcquireURI()
	defer fasthttp.ReleaseURI(subdomain)
	require.NoError(t, subdomain.Parse(nil, []byte("http://attacker.example.com/")))
	require.Empty(t, jar.Get(subdomain))
}

func Test_CookieJar_HostOnlyCookieMatchesMixedCaseHost(t *testing.T) {
	t.Parallel()

	jar := &CookieJar{}

	origin := fasthttp.AcquireURI()
	defer fasthttp.ReleaseURI(origin)
	require.NoError(t, origin.Parse(nil, []byte("http://example.com/")))

	c := &fasthttp.Cookie{}
	c.SetKey("sid")
	c.SetValue("123")
	jar.Set(origin, c)

	mixedCaseHost := fasthttp.AcquireURI()
	defer fasthttp.ReleaseURI(mixedCaseHost)
	require.NoError(t, mixedCaseHost.Parse(nil, []byte("http://Example.com/")))

	require.Equal(t, []string{"sid"}, cookieKeys(jar.Get(mixedCaseHost)))
}

func Test_CookieJar_RejectUnrelatedResponseDomain(t *testing.T) {
	t.Parallel()

	jar := &CookieJar{}
	resp := fasthttp.AcquireResponse()
	host := []byte("attacker.invalid")

	c := &fasthttp.Cookie{}
	c.SetKey("sess")
	c.SetValue("evil")
	c.SetDomain("victim.example")
	resp.Header.SetCookie(c)

	jar.parseCookiesFromResp(host, nil, resp)

	uri := fasthttp.AcquireURI()
	require.NoError(t, uri.Parse(nil, []byte("http://victim.example/")))
	require.Empty(t, jar.Get(uri))
}

func Test_CookieJar_SetRejectUnrelatedDomain(t *testing.T) {
	t.Parallel()

	jar := &CookieJar{}
	origin := fasthttp.AcquireURI()
	defer fasthttp.ReleaseURI(origin)
	require.NoError(t, origin.Parse(nil, []byte("http://attacker.example/")))

	c := &fasthttp.Cookie{}
	c.SetKey("sess")
	c.SetValue("evil")
	c.SetDomain("victim.example")

	jar.Set(origin, c)

	target := fasthttp.AcquireURI()
	defer fasthttp.ReleaseURI(target)
	require.NoError(t, target.Parse(nil, []byte("http://victim.example/")))
	require.Empty(t, jar.Get(target))
}

func Test_CookieJar_RejectPublicSuffixResponseDomain(t *testing.T) {
	t.Parallel()

	jar := &CookieJar{}
	resp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseResponse(resp)

	c := &fasthttp.Cookie{}
	c.SetKey("sess")
	c.SetValue("evil")
	c.SetDomain("com")
	resp.Header.SetCookie(c)

	jar.parseCookiesFromResp([]byte("attacker.com"), nil, resp)

	require.Empty(t, jar.hostCookies)
}

func Test_CookieJar_ExactPublicSuffixDomainDowngradedToHostOnly(t *testing.T) {
	t.Parallel()

	jar := &CookieJar{}
	resp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseResponse(resp)

	c := &fasthttp.Cookie{}
	c.SetKey("sess")
	c.SetValue("ok")
	c.SetDomain("com")
	resp.Header.SetCookie(c)

	jar.parseCookiesFromResp([]byte("com"), nil, resp)
	require.Len(t, jar.hostCookies["com"], 1)
	require.True(t, jar.hostCookies["com"][0].isHostOnly)

	origin := fasthttp.AcquireURI()
	defer fasthttp.ReleaseURI(origin)
	require.NoError(t, origin.Parse(nil, []byte("http://com/")))
	require.Equal(t, []string{"sess"}, cookieKeys(jar.Get(origin)))

	other := fasthttp.AcquireURI()
	defer fasthttp.ReleaseURI(other)
	require.NoError(t, other.Parse(nil, []byte("http://example.com/")))
	require.Empty(t, jar.Get(other))
}

func Test_CookieJar_RejectIPAddressSuffixResponseDomain(t *testing.T) {
	t.Parallel()

	jar := &CookieJar{}
	resp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseResponse(resp)

	c := &fasthttp.Cookie{}
	c.SetKey("sess")
	c.SetValue("evil")
	c.SetDomain("2.3.4")
	resp.Header.SetCookie(c)

	jar.parseCookiesFromResp([]byte("1.2.3.4"), nil, resp)

	require.Empty(t, jar.hostCookies)
}

func Test_CookieJar_ExactIPAddressDomainDowngradedToHostOnly(t *testing.T) {
	t.Parallel()

	jar := &CookieJar{}
	resp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseResponse(resp)

	c := &fasthttp.Cookie{}
	c.SetKey("sess")
	c.SetValue("ok")
	c.SetDomain("127.0.0.1")
	resp.Header.SetCookie(c)

	jar.parseCookiesFromResp([]byte("127.0.0.1"), nil, resp)
	require.Len(t, jar.hostCookies["127.0.0.1"], 1)
	require.True(t, jar.hostCookies["127.0.0.1"][0].isHostOnly)

	origin := fasthttp.AcquireURI()
	defer fasthttp.ReleaseURI(origin)
	require.NoError(t, origin.Parse(nil, []byte("http://127.0.0.1/")))
	require.Equal(t, []string{"sess"}, cookieKeys(jar.Get(origin)))

	other := fasthttp.AcquireURI()
	defer fasthttp.ReleaseURI(other)
	require.NoError(t, other.Parse(nil, []byte("http://evil.127.0.0.1/")))
	require.Empty(t, jar.Get(other))
}

func Test_CookieJar_RejectIPAddressResponseDomainFromHostname(t *testing.T) {
	t.Parallel()

	jar := &CookieJar{}
	resp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseResponse(resp)

	c := &fasthttp.Cookie{}
	c.SetKey("sess")
	c.SetValue("evil")
	c.SetDomain("127.0.0.1")
	resp.Header.SetCookie(c)

	jar.parseCookiesFromResp([]byte("evil.127.0.0.1"), nil, resp)

	require.Empty(t, jar.hostCookies)

	uri := fasthttp.AcquireURI()
	defer fasthttp.ReleaseURI(uri)
	require.NoError(t, uri.Parse(nil, []byte("http://127.0.0.1/")))
	require.Empty(t, jar.Get(uri))
}

func Test_CookieJar_SetRejectIPAddressDomainFromHostname(t *testing.T) {
	t.Parallel()

	jar := &CookieJar{}
	origin := fasthttp.AcquireURI()
	defer fasthttp.ReleaseURI(origin)
	require.NoError(t, origin.Parse(nil, []byte("http://evil.127.0.0.1/")))

	c := &fasthttp.Cookie{}
	c.SetKey("sess")
	c.SetValue("evil")
	c.SetDomain("127.0.0.1")

	jar.Set(origin, c)

	require.Empty(t, jar.hostCookies)

	target := fasthttp.AcquireURI()
	defer fasthttp.ReleaseURI(target)
	require.NoError(t, target.Parse(nil, []byte("http://127.0.0.1/")))
	require.Empty(t, jar.Get(target))
}

func Test_CookieJar_ResponseDomainCookieSentToMatchingSiblingSubdomain(t *testing.T) {
	t.Parallel()

	jar := &CookieJar{}
	resp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseResponse(resp)

	c := &fasthttp.Cookie{}
	c.SetKey("sess")
	c.SetValue("shared")
	c.SetDomain("example.com")
	resp.Header.SetCookie(c)

	jar.parseCookiesFromResp([]byte("sub.example.com"), nil, resp)

	other := fasthttp.AcquireURI()
	defer fasthttp.ReleaseURI(other)
	require.NoError(t, other.Parse(nil, []byte("http://other.example.com/")))
	require.Equal(t, []string{"sess"}, cookieKeys(jar.Get(other)))
}

func Test_CookieJar_TrailingDotDomainDowngradedToHostOnly(t *testing.T) {
	t.Parallel()

	jar := &CookieJar{}
	resp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseResponse(resp)

	c := &fasthttp.Cookie{}
	c.SetKey("sess")
	c.SetValue("123")
	c.SetDomain("example.com.")
	resp.Header.SetCookie(c)

	jar.parseCookiesFromResp([]byte("sub.example.com."), nil, resp)
	require.Len(t, jar.hostCookies["sub.example.com."], 1)
	require.True(t, jar.hostCookies["sub.example.com."][0].isHostOnly)

	origin := fasthttp.AcquireURI()
	defer fasthttp.ReleaseURI(origin)
	require.NoError(t, origin.Parse(nil, []byte("http://sub.example.com./")))
	require.Equal(t, []string{"sess"}, cookieKeys(jar.Get(origin)))

	other := fasthttp.AcquireURI()
	defer fasthttp.ReleaseURI(other)
	require.NoError(t, other.Parse(nil, []byte("http://other.example.com./")))
	require.Empty(t, jar.Get(other))
}

func Test_CookieJar_TrailingDotDomainDowngradedToHostOnlyOnPlainHost(t *testing.T) {
	t.Parallel()

	jar := &CookieJar{}
	resp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseResponse(resp)

	c := &fasthttp.Cookie{}
	c.SetKey("sess")
	c.SetValue("123")
	c.SetDomain("example.com.")
	resp.Header.SetCookie(c)

	jar.parseCookiesFromResp([]byte("sub.example.com"), nil, resp)
	require.Len(t, jar.hostCookies["sub.example.com"], 1)
	require.True(t, jar.hostCookies["sub.example.com"][0].isHostOnly)

	origin := fasthttp.AcquireURI()
	defer fasthttp.ReleaseURI(origin)
	require.NoError(t, origin.Parse(nil, []byte("http://sub.example.com/")))
	require.Equal(t, []string{"sess"}, cookieKeys(jar.Get(origin)))

	other := fasthttp.AcquireURI()
	defer fasthttp.ReleaseURI(other)
	require.NoError(t, other.Parse(nil, []byte("http://other.example.com/")))
	require.Empty(t, jar.Get(other))
}

func Test_CookieJar_MixedHostOnlyAndDomainCookies(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name  string
		order []string
	}{
		{
			name:  "host-only first",
			order: []string{"host-only", "domain"},
		},
		{
			name:  "domain first",
			order: []string{"domain", "host-only"},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			jar := &CookieJar{}

			hostOnlyOrigin := fasthttp.AcquireURI()
			defer fasthttp.ReleaseURI(hostOnlyOrigin)
			require.NoError(t, hostOnlyOrigin.Parse(nil, []byte("http://example.com/")))

			domainOrigin := fasthttp.AcquireURI()
			defer fasthttp.ReleaseURI(domainOrigin)
			require.NoError(t, domainOrigin.Parse(nil, []byte("http://sub.example.com/")))

			hostOnlyCookie := &fasthttp.Cookie{}
			hostOnlyCookie.SetKey("host-only")
			hostOnlyCookie.SetValue("123")

			domainCookie := &fasthttp.Cookie{}
			domainCookie.SetKey("domain")
			domainCookie.SetValue("456")
			domainCookie.SetDomain("example.com")

			for _, cookieType := range testCase.order {
				switch cookieType {
				case "host-only":
					jar.Set(hostOnlyOrigin, hostOnlyCookie)
				case "domain":
					jar.Set(domainOrigin, domainCookie)
				default:
					t.Fatalf("unexpected cookie type %q", cookieType)
				}
			}

			anotherSubdomain := fasthttp.AcquireURI()
			defer fasthttp.ReleaseURI(anotherSubdomain)
			require.NoError(t, anotherSubdomain.Parse(nil, []byte("http://child.example.com/")))
			require.Equal(t, []string{"domain"}, cookieKeys(jar.Get(anotherSubdomain)))

			require.ElementsMatch(t, []string{"domain", "host-only"}, cookieKeys(jar.Get(hostOnlyOrigin)))
		})
	}
}

func Test_CookieJar_Secure(t *testing.T) {
	t.Parallel()

	jar := &CookieJar{}

	uriHTTP := fasthttp.AcquireURI()
	require.NoError(t, uriHTTP.Parse(nil, []byte("http://example.com/")))

	c := &fasthttp.Cookie{}
	c.SetKey("k")
	c.SetValue("v")
	c.SetSecure(true)

	jar.Set(uriHTTP, c)

	cookies := jar.Get(uriHTTP)
	require.Empty(t, cookies)

	uriHTTPS := fasthttp.AcquireURI()
	require.NoError(t, uriHTTPS.Parse(nil, []byte("https://example.com/")))

	cookies = jar.Get(uriHTTPS)
	require.Len(t, cookies, 1)
	require.Equal(t, "k", string(cookies[0].Key()))
	require.Equal(t, "v", string(cookies[0].Value()))
}

func Test_CookieJar_PathMatch(t *testing.T) {
	t.Parallel()

	jar := &CookieJar{}

	setURI := fasthttp.AcquireURI()
	require.NoError(t, setURI.Parse(nil, []byte("http://example.com/api")))

	c := &fasthttp.Cookie{}
	c.SetKey("k")
	c.SetValue("v")
	c.SetPath("/api")

	jar.Set(setURI, c)

	uriExact := fasthttp.AcquireURI()
	require.NoError(t, uriExact.Parse(nil, []byte("http://example.com/api")))
	require.Len(t, jar.Get(uriExact), 1)

	uriChild := fasthttp.AcquireURI()
	require.NoError(t, uriChild.Parse(nil, []byte("http://example.com/api/v1")))
	require.Len(t, jar.Get(uriChild), 1)

	uriNoMatch := fasthttp.AcquireURI()
	require.NoError(t, uriNoMatch.Parse(nil, []byte("http://example.com/apiv1")))
	require.Empty(t, jar.Get(uriNoMatch))
}

// Test_CookieJar_DomainMatchBoundary pins the RFC 6265 §5.1.3 label-boundary
// semantics of domainMatch: a bare string suffix without a '.' separator must
// never match, and the comparison is ASCII case-insensitive.
func Test_CookieJar_DomainMatchBoundary(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		host, domain string
		want         bool
	}{
		{"example.com", "example.com", true},
		{"sub.example.com", "example.com", true},
		{"deep.sub.example.com", "example.com", true},
		// Suffix overlap without a label boundary must not match.
		{"evilexample.com", "example.com", false},
		{"xample.com", "example.com", false},
		// Domain longer than host never matches.
		{"example.com", "sub.example.com", false},
		{"com", "example.com", false},
		// ASCII case-insensitive on both sides.
		{"EXAMPLE.com", "example.com", true},
		{"sub.EXAMPLE.com", "example.COM", true},
		{"evilEXAMPLE.com", "example.com", false},
	}
	for _, tc := range testCases {
		require.Equal(t, tc.want, domainMatch(tc.host, tc.domain), "domainMatch(%q, %q)", tc.host, tc.domain)
	}
}

// Test_CookieJar_DistinctPathsCoexist covers RFC 6265 Section 5.3 step 11:
// a stored cookie is only replaced when name, domain and path all match. A
// cookie set for a deeper path must not evict the same-named cookie stored
// for a shallower one.
func Test_CookieJar_DistinctPathsCoexist(t *testing.T) {
	t.Parallel()

	jar := AcquireCookieJar()
	defer ReleaseCookieJar(jar)

	root := fasthttp.AcquireCookie()
	defer fasthttp.ReleaseCookie(root)
	root.SetKey("a")
	root.SetValue("root")
	root.SetPath("/")

	admin := fasthttp.AcquireCookie()
	defer fasthttp.ReleaseCookie(admin)
	admin.SetKey("a")
	admin.SetValue("admin")
	admin.SetPath("/admin")

	jar.SetByHost([]byte("example.com"), root)
	jar.SetByHost([]byte("example.com"), admin)

	collect := func(path string) map[string]string {
		got := jar.getByHostAndPath([]byte("example.com"), []byte(path), false)
		out := make(map[string]string, len(got))
		for _, c := range got {
			out[string(c.Path())] = string(c.Value())
			fasthttp.ReleaseCookie(c)
		}
		return out
	}

	require.Equal(t, map[string]string{"/": "root"}, collect("/"))
	require.Equal(t, map[string]string{"/": "root", "/admin": "admin"}, collect("/admin"))
	require.Equal(t, map[string]string{"/": "root"}, collect("/other"))

	// Re-setting the same (name, path) replaces in place rather than appending.
	updated := fasthttp.AcquireCookie()
	defer fasthttp.ReleaseCookie(updated)
	updated.SetKey("a")
	updated.SetValue("root2")
	updated.SetPath("/")
	jar.SetByHost([]byte("example.com"), updated)

	require.Equal(t, map[string]string{"/": "root2"}, collect("/"))
	require.Equal(t, map[string]string{"/": "root2", "/admin": "admin"}, collect("/admin"))
}

// Test_CookieJar_SetByHost_DoesNotMutateArgument checks the jar's documented
// contract that it only stores copies: normalizing the Domain attribute used
// to case-fold the caller's cookie in place.
func Test_CookieJar_SetByHost_DoesNotMutateArgument(t *testing.T) {
	t.Parallel()

	jar := AcquireCookieJar()
	defer ReleaseCookieJar(jar)

	c := fasthttp.AcquireCookie()
	defer fasthttp.ReleaseCookie(c)
	c.SetKey("a")
	c.SetValue("1")
	c.SetDomain("Example.COM")

	jar.SetByHost([]byte("sub.example.com"), c)

	require.Equal(t, "Example.COM", string(c.Domain()))

	// The stored copy is still normalized, so lookups keep working.
	got := jar.getByHostAndPath([]byte("sub.example.com"), []byte("/"), false)
	require.Len(t, got, 1)
	require.Equal(t, "1", string(got[0].Value()))
	fasthttp.ReleaseCookie(got[0])
}

// Test_CookieJar_MatchesStdlibJar cross-checks storage and retrieval against
// net/http/cookiejar, which implements RFC 6265 with the same public-suffix
// list. It caught path-less cookies being stored at "/" instead of the
// request's default-path.
func Test_CookieJar_MatchesStdlibJar(t *testing.T) {
	t.Parallel()

	type setStep struct {
		url    string
		cookie string
	}
	tests := []struct {
		name string
		sets []setStep
		gets []string
	}{
		{"host only", []setStep{{"http://example.com/", "a=1"}},
			[]string{"http://example.com/", "http://sub.example.com/", "http://other.com/"}},
		{"domain attribute", []setStep{{"http://example.com/", "a=1; Domain=example.com"}},
			[]string{"http://example.com/", "http://sub.example.com/"}},
		{"leading dot domain", []setStep{{"http://example.com/", "a=1; Domain=.example.com"}},
			[]string{"http://example.com/", "http://sub.example.com/"}},
		{"subdomain sets parent", []setStep{{"http://sub.example.com/", "a=1; Domain=example.com"}},
			[]string{"http://example.com/", "http://sub.example.com/", "http://x.example.com/"}},
		{"public suffix rejected", []setStep{{"http://example.com/", "a=1; Domain=com"}},
			[]string{"http://example.com/", "http://other.com/"}},
		{"unrelated domain rejected", []setStep{{"http://example.com/", "a=1; Domain=evil.com"}},
			[]string{"http://example.com/", "http://evil.com/"}},
		{"explicit paths", []setStep{{"http://example.com/", "a=1; Path=/"}, {"http://example.com/admin", "b=2; Path=/admin"}},
			[]string{"http://example.com/", "http://example.com/admin", "http://example.com/admin/x", "http://example.com/adminx"}},
		{"secure", []setStep{{"https://example.com/", "a=1; Secure"}},
			[]string{"https://example.com/", "http://example.com/"}},
		{"overwrite", []setStep{{"http://example.com/", "a=1"}, {"http://example.com/", "a=2"}},
			[]string{"http://example.com/"}},
		{"ip host", []setStep{{"http://127.0.0.1/", "a=1"}}, []string{"http://127.0.0.1/"}},
		{"ip domain", []setStep{{"http://127.0.0.1/", "a=1; Domain=127.0.0.1"}}, []string{"http://127.0.0.1/"}},
		{"default path", []setStep{{"http://example.com/a/b", "a=1"}},
			[]string{"http://example.com/a/b", "http://example.com/a/", "http://example.com/a", "http://example.com/"}},
		{"default path at root", []setStep{{"http://example.com/b", "a=1"}},
			[]string{"http://example.com/b", "http://example.com/"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			std, err := cookiejar.New(&cookiejar.Options{PublicSuffixList: publicsuffix.List})
			require.NoError(t, err)
			jar := AcquireCookieJar()
			defer ReleaseCookieJar(jar)

			for _, s := range tt.sets {
				u, err := url.Parse(s.url)
				require.NoError(t, err)

				header := http.Header{}
				header.Add("Set-Cookie", s.cookie)
				std.SetCookies(u, (&http.Response{Header: header}).Cookies())

				resp := fasthttp.AcquireResponse()
				resp.Header.Add("Set-Cookie", s.cookie)
				jar.parseCookiesFromResp([]byte(u.Host), []byte(u.Path), resp)
				fasthttp.ReleaseResponse(resp)
			}

			for _, g := range tt.gets {
				u, err := url.Parse(g)
				require.NoError(t, err)

				want := make([]string, 0, 2)
				for _, c := range std.Cookies(u) {
					want = append(want, c.Name+"="+c.Value)
				}
				sort.Strings(want)

				fURI := fasthttp.AcquireURI()
				require.NoError(t, fURI.Parse(nil, []byte(g)))
				got := make([]string, 0, 2)
				for _, c := range jar.Get(fURI) {
					got = append(got, string(c.Key())+"="+string(c.Value()))
					fasthttp.ReleaseCookie(c)
				}
				fasthttp.ReleaseURI(fURI)
				sort.Strings(got)

				require.Equal(t, want, got, "request %s", g)
			}
		})
	}
}
