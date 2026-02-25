package miservice

import (
	"crypto/md5"
	"crypto/sha1"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"ninego/log"
	"strings"
)

type Account struct {
	client     *http.Client
	username   string
	password   string
	tokenStore TokenStore
	token      *Tokens
}

func NewAccount(username string, password string, tokenStore TokenStore) *Account {
	j, _ := cookiejar.New(nil)
	return &Account{
		client: &http.Client{
			Jar: j,
		},
		username:   username,
		password:   password,
		tokenStore: tokenStore,
	}
}

// sid: service id, like "xiaomiio", "micoapi", "mina"
func (ma *Account) Login(sid string) error {
	var err error
	defer func() {
		if err != nil && ma.tokenStore != nil {
			ma.tokenStore.SaveToken(nil)
		}
	}()
	if ma.token == nil {
		if ma.tokenStore != nil {
			tokens, err := ma.tokenStore.LoadToken()
			if err == nil {
				if tokens.UserName != ma.username {
					ma.tokenStore.SaveToken(nil)
				} else {
					ma.token = tokens
				}
			}
		}
	}
	if ma.token == nil {
		ma.token = NewTokens()
		ma.token.UserName = ma.username
		ma.token.DeviceId = strings.ToUpper(getRandom(16))
	}

	cookies := []*http.Cookie{
		{Name: "sdkVersion", Value: "3.9"},
		{Name: "deviceId", Value: ma.token.DeviceId},
	}

	if ma.token.PassToken != "" {
		cookies = append(cookies, &http.Cookie{Name: "userId", Value: ma.token.UserId})
		cookies = append(cookies, &http.Cookie{Name: "passToken", Value: ma.token.PassToken})
	}

	var resp *loginResp
	resp, err = ma.serviceLogin(fmt.Sprintf("serviceLogin?sid=%s&_json=true", sid), nil, cookies)
	if err != nil {
		return err
	}

	if resp.Code != 0 {
		data := url.Values{
			"_json":    {"true"},
			"qs":       {resp.Qs},
			"sid":      {resp.Sid},
			"_sign":    {resp.Sign},
			"callback": {resp.Callback},
			"user":     {ma.username},
			"hash":     {strings.ToUpper(fmt.Sprintf("%x", md5.Sum([]byte(ma.password))))},
		}
		resp, err = ma.serviceLogin("serviceLoginAuth2", data, cookies)
		if err != nil {
			log.Error("serviceLoginAuth2 error", err)
			return err
		}
		if resp.Code != 0 {
			return errors.New(fmt.Sprintf("Code Error: %v", resp))
		}
	}
	ma.token.UserId = fmt.Sprint(resp.UserID)
	ma.token.PassToken = resp.PassToken

	var serviceToken string
	serviceToken, err = ma.securityTokenService(resp.Location, resp.Ssecurity, resp.Nonce)
	if err != nil {
		log.Error("securityTokenService error", err)
		return err
	}
	ma.token.Sids[sid] = SidToken{
		Ssecurity:    resp.Ssecurity,
		ServiceToken: serviceToken,
	}

	if ma.tokenStore != nil {
		ma.tokenStore.SaveToken(ma.token)
	}

	return nil
}

type loginResp struct {
	Qs             string      `json:"qs"`
	Ssecurity      string      `json:"ssecurity"`
	Code           int         `json:"code"`
	PassToken      string      `json:"passToken"`
	Description    string      `json:"description"`
	SecurityStatus int         `json:"securityStatus"`
	Nonce          int64       `json:"nonce"`
	UserID         int64       `json:"userId"`
	CUserID        string      `json:"cUserId"`
	Result         string      `json:"result"`
	Psecurity      string      `json:"psecurity"`
	CaptchaURL     interface{} `json:"captchaUrl"`
	Location       string      `json:"location"`
	Pwd            int         `json:"pwd"`
	Child          int         `json:"child"`
	Desc           string      `json:"desc"`

	ServiceParam string `json:"serviceParam"`
	Sign         string `json:"_sign"`
	Sid          string `json:"sid"`
	Callback     string `json:"callback"`
}

func (ma *Account) serviceLogin(uri string, data url.Values, cookies []*http.Cookie) (*loginResp, error) {
	headers := http.Header{
		"User-Agent": []string{UA},
	}
	var reqBody io.Reader
	method := http.MethodGet
	if data != nil {
		reqBody = strings.NewReader(data.Encode())
		method = http.MethodPost
		headers.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	req, _ := http.NewRequest(method, "https://account.xiaomi.com/pass/"+uri, reqBody)
	req.Header = headers

	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
	log.Debug("service login", req.URL.String())
	resp, err := ma.client.Do(req)
	if err != nil {
		log.Error("http do request error", err)
		return nil, err
	}
	defer resp.Body.Close()
	log.Debug("service login return", resp.StatusCode)

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	log.Debug("body", string(body))
	var jsonResponse loginResp
	err = json.Unmarshal(body[11:], &jsonResponse)
	if err != nil {
		log.Error("json unmarshal error", err, string(body))
		return nil, err
	}
	log.Debug("service login success", jsonResponse)
	return &jsonResponse, nil
}

func secureUrl(location, ssecurity string, nonce int64) string {
	nsec := fmt.Sprintf("nonce=%d&%s", nonce, ssecurity)
	sum := sha1.Sum([]byte(nsec))
	clientSign := base64.StdEncoding.EncodeToString(sum[:])
	es := url.QueryEscape(clientSign)
	//es = strings.ReplaceAll(es, "%2F", "/")
	requestUrl := fmt.Sprintf("%s&clientSign=%s", location, es)
	return requestUrl
}

func (ma *Account) securityTokenService(location, ssecurity string, nonce int64) (string, error) {
	requestUrl := secureUrl(location, ssecurity, nonce)
	log.Debug("securityTokenService", requestUrl)
	req, _ := http.NewRequest(http.MethodGet, requestUrl, nil)
	headers := http.Header{
		"User-Agent": []string{UA},
	}
	req.Header = headers

	resp, err := ma.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	cookies := resp.Cookies()
	var serviceToken string

	for _, cookie := range cookies {
		if cookie.Name == "serviceToken" {
			serviceToken = cookie.Value
			break
		}
	}

	if serviceToken == "" {
		body, _ := io.ReadAll(resp.Body)
		return "", errors.New(string(body))
	}

	return serviceToken, nil
}

type DataCb func(tokens *Tokens, cookie map[string]string) url.Values

func (ma *Account) NewRequest(sid, u string, data url.Values, cb DataCb, headers http.Header) *http.Request {
	var req *http.Request
	var body io.Reader
	cookies := []*http.Cookie{
		{Name: "userId", Value: ma.token.UserId},
		{Name: "serviceToken", Value: ma.token.Sids[sid].ServiceToken},
	}
	//fmt.Println("tokens", ma.token)

	method := http.MethodGet
	if data != nil || cb != nil {
		var vals url.Values
		if cb != nil {
			var cookieMap = make(map[string]string)
			vals = cb(ma.token, cookieMap)
			for k, v := range cookieMap {
				cookies = append(cookies, &http.Cookie{Name: k, Value: v})
			}
		} else if data != nil {
			vals = data
		}
		if vals != nil {
			method = http.MethodPost
			log.Debug("request data", vals.Encode())
			body = strings.NewReader(vals.Encode())
			headers.Set("Content-Type", "application/x-www-form-urlencoded")
		}
	}
	req, _ = http.NewRequest(method, u, body)
	if headers != nil {
		req.Header = headers
	}
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
	/*for k, v := range cookies {
		log.Println("request cookie", k, v)
	}*/
	log.Debug("request cookie", cookies)
	return req
}

func (ma *Account) hasSid(sid string) bool {
	if ma.token == nil {
		return false
	}
	_, ok := ma.token.Sids[sid]
	return ok
}

func (ma *Account) Request(sid, u string, data url.Values, cb DataCb, headers http.Header, reLogin bool, output any) error {
	if !ma.hasSid(sid) {
		err := ma.Login(sid)
		if err != nil {
			return err
		}
	}
	log.Debug("request token done")
	req := ma.NewRequest(sid, u, data, cb, headers)
	resp, err := ma.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		type _result struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		}
		data, err := io.ReadAll(resp.Body)
		if err != nil {
			return err
		}
		log.Debug("response", u, string(data))
		var result _result
		err = json.Unmarshal(data, &result)
		if err != nil {
			return err
		}
		if result.Code == 0 {
			err = json.Unmarshal(data, output)
			return err
		}

		if strings.Contains(strings.ToLower(result.Message), "auth") {
			resp.StatusCode = http.StatusUnauthorized
		}
	}

	if resp.StatusCode == http.StatusUnauthorized && reLogin {
		ma.token = nil
		if ma.tokenStore != nil {
			ma.tokenStore.SaveToken(nil)
		}
		return ma.Request(sid, u, data, cb, headers, false, output)
	}

	body, _ := io.ReadAll(resp.Body)
	return errors.New(fmt.Sprintf("Error %s: %s", u, string(body)))
}

const UA = "APP/com.xiaomi.mihome APPV/6.0.103 iosPassportSDK/3.9.0 iOS/14.4 miHSTS"
