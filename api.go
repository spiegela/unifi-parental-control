package main

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
)

// API is an interface to a UniFi controller.
type API struct {
	httpClient *http.Client
	cookieBase *url.URL
	authStore  AuthStore
	auth       *Auth
}

// Auth holds the authentication information for accessing a UniFi controller.
type Auth struct {
	Username       string
	Password       string
	ControllerHost string
	Cookies        []*http.Cookie
}

// NewAPI constructs a new API.
func NewAPI(authStore AuthStore) (*API, error) {
	auth, err := authStore.Load()
	if err != nil {
		return nil, err
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, err
	}
	cookieBase := &url.URL{
		Scheme: "https",
		Host:   auth.ControllerHost,
	}
	jar.SetCookies(cookieBase, auth.Cookies)

	api := &API{
		httpClient: &http.Client{
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{
					// TODO: support proper certs
					InsecureSkipVerify: true,
				},
			},
			Jar: jar,
		},
		cookieBase: cookieBase,
		authStore:  authStore,
		auth:       auth,
	}
	return api, nil
}

// WriteConfig writes the configuration to the configured AuthStore.
func (api *API) WriteConfig() error {
	api.auth.Cookies = api.httpClient.Jar.Cookies(api.cookieBase)
	return api.authStore.Save(api.auth)
}

func (api *API) post(u string, src, dst interface{}, opts reqOpts) error {
	u = api.baseURL() + u
	body, err := json.Marshal(src)
	if err != nil {
		panic("internal error marshaling JSON POST body: " + err.Error())
	}
	req, err := http.NewRequest("POST", u, bytes.NewReader(body))
	if err != nil {
		panic("internal error: " + err.Error())
	}
	return api.processHttpRequest(req, dst, opts)
}

func (api *API) get(u string, dst interface{}, opts reqOpts) error {
	u = api.baseURL() + u
	req, err := http.NewRequest("GET", u, nil)
	if err != nil {
		panic("internal error: " + err.Error())
	}
	return api.processHttpRequest(req, dst, opts)
}

type reqOpts struct {
	referer string
}

func (api *API) processHttpRequest(req *http.Request, dst interface{}, opts reqOpts) error {
	if opts.referer != "" {
		req.Header.Set("Referer", opts.referer)
	}

	dec := struct {
		Data interface{} `json:"data"`
		Meta struct {
			Code string `json:"rc"`
			Msg  string `json:"msg"`
		} `json:"meta"`
	}{Data: dst}

	triedLogin := false
	for {
		resp, err := api.httpClient.Do(req)
		if err != nil {
			return err
		}
		body, err := ioutil.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return err
		}

		if err := json.Unmarshal(body, &dec); err != nil {
			return fmt.Errorf("parsing response body: %v", err)
		}

		if resp.StatusCode == 200 {
			if dec.Meta.Code != "ok" {
				return fmt.Errorf("non-ok return code %q (%s)", dec.Meta.Code, dec.Meta.Msg)
			}
			return nil
		}

		if resp.StatusCode == http.StatusUnauthorized && !triedLogin { // 401
			if dec.Meta.Code == "error" && dec.Meta.Msg == "api.err.LoginRequired" {
				if err := api.login(); err != nil {
					return err
				}
				triedLogin = true
				continue
			}
		}

		return fmt.Errorf("HTTP response %s", resp.Status)
	}
}

func (api *API) baseURL() string {
	return "https://" + api.auth.ControllerHost + ":8443"
}

func (api *API) login() error {
	req := struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}{
		Username: api.auth.Username,
		Password: api.auth.Password,
	}
	return api.post("/api/login", &req, &json.RawMessage{}, reqOpts{
		referer: api.baseURL() + "/login",
	})
}

func (api *API) ListClients(site string) ([]Client, error) {
	var resp []Client
	if err := api.get("/api/s/"+site+"/stat/sta", &resp, reqOpts{}); err != nil {
		return nil, err
	}
	return resp, nil
}

func (api *API) BlockClient(site string, mac string) error {
	request := struct {
		Cmd string `json:"cmd"`
		Mac string `json:"mac"`
	}{
		Cmd: "block-sta",
		Mac: strings.ToUpper(mac),
	}
	err := api.post("/api/s/"+site+"/cmd/stamgr", &request, &json.RawMessage{}, reqOpts{})
	if err != nil {
		return err
	}
	return nil
}

func (api *API) UnblockClient(site string, mac string) error {
	request := struct {
		Cmd string `json:"cmd"`
		Mac string `json:"mac"`
	}{
		Cmd: "unblock-sta", //only diff with above function
		Mac: strings.ToUpper(mac),
	}
	err := api.post("/api/s/"+site+"/cmd/stamgr", &request, &json.RawMessage{}, reqOpts{})
	if err != nil {
		return err
	}
	return nil
}

func (api *API) ListWirelessNetworks(site string) ([]WirelessNetwork, error) {
	var resp []WirelessNetwork
	err := api.get("/api/s/"+site+"/list/wlanconf", &resp, reqOpts{})
	if err != nil {
		return nil, err
	}
	return resp, nil
}

func (api *API) EnableWirelessNetwork(site, id string, enable bool) error {
	req := struct {
		Enabled bool `json:"enabled"`
	}{enable}
	return api.post("/api/s/"+site+"/upd/wlanconf/"+id, &req, &json.RawMessage{}, reqOpts{})
}