package appstore

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v4"
)

const (
	HostSandBox    = "https://api.storekit-sandbox.itunes.apple.com"
	HostProduction = "https://api.storekit.itunes.apple.com"

	PathTransactionInfo                     = "/inApps/v1/transactions/{transactionId}"
	PathLookUp                              = "/inApps/v1/lookup/{orderId}"
	PathTransactionHistory                  = "/inApps/v1/history/{originalTransactionId}"
	PathRefundHistory                       = "/inApps/v2/refund/lookup/{originalTransactionId}"
	PathGetALLSubscriptionStatus            = "/inApps/v1/subscriptions/{originalTransactionId}"
	PathConsumptionInfo                     = "/inApps/v1/transactions/consumption/{originalTransactionId}"
	PathExtendSubscriptionRenewalDate       = "/inApps/v1/subscriptions/extend/{originalTransactionId}"
	PathExtendSubscriptionRenewalDateForAll = "/inApps/v1/subscriptions/extend/mass/"
	PathGetStatusOfSubscriptionRenewalDate  = "/inApps/v1/subscriptions/extend/mass/{productId}/{requestIdentifier}"
	PathGetNotificationHistory              = "/inApps/v1/notifications/history"
	PathRequestTestNotification             = "/inApps/v1/notifications/test"
	PathGetTestNotificationStatus           = "/inApps/v1/notifications/test/{testNotificationToken}"
)

type StoreConfig struct {
	KeyContent []byte // Loads a .p8 certificate
	KeyID      string // Your private key ID from App Store Connect (Ex: 2X9R4HXF34)
	BundleID   string // Your app’s bundle ID
	Issuer     string // Your issuer ID from the Keys page in App Store Connect (Ex: "57246542-96fe-1a63-e053-0824d011072a")
	Sandbox    bool   // default is Production
}

type StoreClient struct {
	Token   *Token
	httpCli *http.Client
	cert    *Cert
	hostUrl string
}

// NewStoreClient create a appstore server api client
func NewStoreClient(config *StoreConfig) *StoreClient {
	token := &Token{}
	token.WithConfig(config)
	hostUrl := HostProduction
	if config.Sandbox {
		hostUrl = HostSandBox
	}

	client := &StoreClient{
		Token: token,
		cert:  &Cert{},
		httpCli: &http.Client{
			Timeout: 30 * time.Second,
		},
		hostUrl: hostUrl,
	}
	return client
}

// NewStoreClientWithHTTPClient creates a appstore server api client with a custom http client.
func NewStoreClientWithHTTPClient(config *StoreConfig, httpClient *http.Client) *StoreClient {
	token := &Token{}
	token.WithConfig(config)
	hostUrl := HostProduction
	if config.Sandbox {
		hostUrl = HostSandBox
	}

	client := &StoreClient{
		Token:   token,
		cert:    &Cert{},
		httpCli: httpClient,
		hostUrl: hostUrl,
	}
	return client
}

func (c *StoreClient) httpClient() HTTPClient {
	var client DoFunc = func(req *http.Request) (*http.Response, error) {
		authToken, err := c.Token.GenerateIfExpired()
		if err != nil {
			return nil, fmt.Errorf("appstore generate token err %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+authToken)
		req.Header.Set("User-Agent", "App Store Client")
		return c.httpCli.Do(req)
	}

	client = SetRetry(client, &JitterBackoff{}, func(i int, err error) bool {
		if i == http.StatusUnauthorized {
			return true
		}
		if errors.Is(err, io.ErrUnexpectedEOF) {
			return true
		}

		if errors.Is(err, io.EOF) {
			return true
		}
		return false
	})
	return client
}

// GetTransactionHistory https://developer.apple.com/documentation/appstoreserverapi/get_transaction_history
func (c *StoreClient) GetTransactionHistory(ctx context.Context, originalTransactionId string, query *url.Values) (responses []*HistoryResponse, err error) {
	URL := c.hostUrl + PathTransactionHistory
	URL = strings.Replace(URL, "{originalTransactionId}", originalTransactionId, -1)

	if query == nil {
		query = &url.Values{}
	}

	client := c.httpClient()
	client = RequireResponseStatus(client, http.StatusOK)

	for {
		rsp := HistoryResponse{}
		client = SetResponseBodyHandler(client, json.Unmarshal, &rsp)
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, URL+"?"+query.Encode(), nil)
		_, err = client.Do(req)
		if err != nil {
			return nil, err
		}

		responses = append(responses, &rsp)
		if !rsp.HasMore {
			return
		}

		if rsp.Revision != "" {
			query.Set("revision", rsp.Revision)
		} else {
			return
		}

		time.Sleep(10 * time.Millisecond)
	}
}

// GetALLSubscriptionStatuses https://developer.apple.com/documentation/appstoreserverapi/get_all_subscription_statuses
func (c *StoreClient) GetALLSubscriptionStatuses(ctx context.Context, originalTransactionId string) (*StatusResponse, error) {
	URL := c.hostUrl + PathGetALLSubscriptionStatus
	URL = strings.Replace(URL, "{originalTransactionId}", originalTransactionId, -1)
	client := c.httpClient()
	client = RequireResponseStatus(client, http.StatusOK)
	rsp := &StatusResponse{}
	client = SetResponseBodyHandler(client, json.Unmarshal, rsp)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, URL, nil)
	_, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	return rsp, nil
}

// GetTransactionInfo https://developer.apple.com/documentation/appstoreserverapi/get_transaction_info
func (c *StoreClient) GetTransactionInfo(ctx context.Context, transactionId string) (*TransactionInfoResponse, error) {
	URL := c.hostUrl + PathTransactionInfo
	URL = strings.Replace(URL, "{transactionId}", transactionId, -1)

	client := c.httpClient()
	client = RequireResponseStatus(client, http.StatusOK)

	rsp := &TransactionInfoResponse{}
	client = SetResponseBodyHandler(client, json.Unmarshal, rsp)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, URL, nil)
	_, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	return rsp, nil
}

// LookupOrderID https://developer.apple.com/documentation/appstoreserverapi/look_up_order_id
func (c *StoreClient) LookupOrderID(ctx context.Context, orderId string) (*OrderLookupResponse, error) {
	URL := c.hostUrl + PathLookUp
	URL = strings.Replace(URL, "{orderId}", orderId, -1)
	rsp := &OrderLookupResponse{}
	client := c.httpClient()
	client = RequireResponseStatus(client, http.StatusOK)
	client = SetResponseBodyHandler(client, json.Unmarshal, rsp)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, URL, nil)
	_, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	return rsp, nil
}

// GetRefundHistory https://developer.apple.com/documentation/appstoreserverapi/get_refund_history
func (c *StoreClient) GetRefundHistory(ctx context.Context, originalTransactionId string) (responses []*RefundLookupResponse, err error) {
	baseURL := c.hostUrl + PathRefundHistory
	baseURL = strings.Replace(baseURL, "{originalTransactionId}", originalTransactionId, -1)

	URL := baseURL
	client := c.httpClient()
	client = RequireResponseStatus(client, http.StatusOK)

	for {
		rsp := RefundLookupResponse{}
		client = SetResponseBodyHandler(client, json.Unmarshal, &rsp)
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, URL, nil)
		_, err = client.Do(req)
		if err != nil {
			return nil, err
		}

		responses = append(responses, &rsp)
		if !rsp.HasMore {
			return
		}

		data := url.Values{}
		if rsp.Revision != "" {
			data.Set("revision", rsp.Revision)
			URL = baseURL + "?" + data.Encode()
		} else {
			return
		}

		time.Sleep(10 * time.Millisecond)
	}
}

// SendConsumptionInfo https://developer.apple.com/documentation/appstoreserverapi/send_consumption_information
func (c *StoreClient) SendConsumptionInfo(ctx context.Context, originalTransactionId string, body ConsumptionRequestBody) (statusCode int, err error) {
	URL := c.hostUrl + PathConsumptionInfo
	URL = strings.Replace(URL, "{originalTransactionId}", originalTransactionId, -1)

	bodyBuf := new(bytes.Buffer)
	err = json.NewEncoder(bodyBuf).Encode(body)
	if err != nil {
		return 0, err
	}

	statusCode, _, err = c.Do(ctx, http.MethodPut, URL, bodyBuf)
	if err != nil {
		return statusCode, err
	}
	return statusCode, nil
}

// ExtendSubscriptionRenewalDate https://developer.apple.com/documentation/appstoreserverapi/extend_a_subscription_renewal_date
func (c *StoreClient) ExtendSubscriptionRenewalDate(ctx context.Context, originalTransactionId string, body ExtendRenewalDateRequest) (statusCode int, err error) {
	URL := c.hostUrl + PathExtendSubscriptionRenewalDate
	URL = strings.Replace(URL, "{originalTransactionId}", originalTransactionId, -1)

	bodyBuf := new(bytes.Buffer)
	err = json.NewEncoder(bodyBuf).Encode(body)
	if err != nil {
		return 0, err
	}

	statusCode, _, err = c.Do(ctx, http.MethodPut, URL, bodyBuf)
	if err != nil {
		return statusCode, err
	}
	return statusCode, nil
}

// ExtendSubscriptionRenewalDateForAll https://developer.apple.com/documentation/appstoreserverapi/extend_subscription_renewal_dates_for_all_active_subscribers
func (c *StoreClient) ExtendSubscriptionRenewalDateForAll(ctx context.Context, body MassExtendRenewalDateRequest) (statusCode int, err error) {
	URL := HostProduction + PathExtendSubscriptionRenewalDateForAll
	if c.Token.Sandbox {
		URL = HostSandBox + PathExtendSubscriptionRenewalDateForAll
	}

	bodyBuf := new(bytes.Buffer)
	err = json.NewEncoder(bodyBuf).Encode(body)
	if err != nil {
		return 0, err
	}

	statusCode, _, err = c.Do(ctx, http.MethodPost, URL, bodyBuf)
	if err != nil {
		return statusCode, err
	}
	return statusCode, nil
}

// GetSubscriptionRenewalDataStatus https://developer.apple.com/documentation/appstoreserverapi/get_status_of_subscription_renewal_date_extensions
func (c *StoreClient) GetSubscriptionRenewalDataStatus(ctx context.Context, productId, requestIdentifier string) (statusCode int, rsp *MassExtendRenewalDateStatusResponse, err error) {
	URL := HostProduction + PathGetStatusOfSubscriptionRenewalDate
	if c.Token.Sandbox {
		URL = HostSandBox + PathGetStatusOfSubscriptionRenewalDate
	}
	URL = strings.Replace(URL, "{productId}", productId, -1)
	URL = strings.Replace(URL, "{requestIdentifier}", requestIdentifier, -1)

	statusCode, body, err := c.Do(ctx, http.MethodGet, URL, nil)
	if err != nil {
		return statusCode, nil, err
	}

	if statusCode != http.StatusOK {
		return statusCode, nil, fmt.Errorf("appstore api: %v return status code %v", URL, statusCode)
	}

	err = json.Unmarshal(body, &rsp)
	if err != nil {
		return statusCode, nil, err
	}

	return statusCode, rsp, nil
}

// GetNotificationHistory https://developer.apple.com/documentation/appstoreserverapi/get_notification_history
func (c *StoreClient) GetNotificationHistory(ctx context.Context, body NotificationHistoryRequest) (responses []NotificationHistoryResponseItem, err error) {
	baseURL := c.hostUrl + PathGetNotificationHistory
	URL := baseURL
	client := c.httpClient()
	client = RequireResponseStatus(client, http.StatusOK)

	for {
		rsp := NotificationHistoryResponses{}
		rsp.NotificationHistory = make([]NotificationHistoryResponseItem, 0)

		client = SetRequestBodyJSON(client, body)
		client = SetResponseBodyHandler(client, json.Unmarshal, &rsp)

		req, _ := http.NewRequestWithContext(ctx, http.MethodPost, URL, nil)
		_, err := client.Do(req)
		if err != nil {
			return nil, err
		}

		responses = append(responses, rsp.NotificationHistory...)
		if !rsp.HasMore {
			return responses, nil
		}

		data := url.Values{}
		if rsp.PaginationToken != "" {
			data.Set("paginationToken", rsp.PaginationToken)
			URL = baseURL + "?" + data.Encode()
		} else {
			return responses, nil
		}

		time.Sleep(10 * time.Millisecond)
	}
}

// SendRequestTestNotification https://developer.apple.com/documentation/appstoreserverapi/request_a_test_notification
func (c *StoreClient) SendRequestTestNotification(ctx context.Context) (*TestNotificationResponse, error) {
	URL := c.hostUrl + PathRequestTestNotification
	var rsp = new(TestNotificationResponse)
	client := c.httpClient()
	client = RequireResponseStatus(client, http.StatusOK, http.StatusNotFound, http.StatusTooManyRequests, http.StatusInternalServerError)
	client = SetResponseBodyHandler(client, json.Unmarshal, rsp)
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, URL, nil)
	_, err := client.Do(req)
	return rsp, err
}

// GetTestNotificationStatus https://developer.apple.com/documentation/appstoreserverapi/get_test_notification_status
func (c *StoreClient) GetTestNotificationStatus(ctx context.Context, testNotificationToken string) (int, []byte, error) {
	URL := c.hostUrl + PathGetTestNotificationStatus
	URL = strings.Replace(URL, "{testNotificationToken}", testNotificationToken, -1)

	return c.Do(ctx, http.MethodGet, URL, nil)
}

func (c *StoreClient) ParseNotificationV2(tokenStr string) (*NotificationPayload, error) {
	var ret = new(NotificationPayload)
	c.parseJWS(tokenStr, ret)
	return ret, c.parseJWS(tokenStr, ret)
}

func (c *StoreClient) ParseNotificationV2WithClaim(tokenStr string) (jwt.Claims, error) {
	result := &jwt.RegisteredClaims{}
	_, err := jwt.ParseWithClaims(tokenStr, result, func(token *jwt.Token) (interface{}, error) {
		return c.cert.extractPublicKeyFromToken(tokenStr)
	})
	return result, err
}

// ParseSignedTransactions parse the jws singed transactions
// Per doc: https://datatracker.ietf.org/doc/html/rfc7515#section-4.1.6
func (c *StoreClient) ParseSignedTransactions(transactions []string) ([]*JWSTransaction, error) {
	result := make([]*JWSTransaction, 0)
	for _, v := range transactions {
		trans, err := c.ParseSignedTransaction(v)
		if err == nil && trans != nil {
			result = append(result, trans)
		}
	}

	return result, nil
}

// ParseJWSEncodeString parse the jws encode string, such as JWSTransaction and JWSRenewalInfoDecodedPayload
func (c *StoreClient) ParseJWSEncodeString(jwsEncode string) (interface{}, error) {
	// Split the JWS format string into its three parts
	parts := strings.Split(jwsEncode, ".")

	// Decode the payload part of the JWS format string
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, err
	}

	// Determine which struct to use based on the payload contents
	if strings.Contains(string(payload), "transactionId") {
		transaction := &JWSTransaction{}
		err = c.parseJWS(jwsEncode, transaction)
		return transaction, err
	} else if strings.Contains(string(payload), "renewalDate") {
		renewalInfo := &JWSRenewalInfoDecodedPayload{}
		err = c.parseJWS(jwsEncode, renewalInfo)
		return renewalInfo, err
	}

	return nil, nil
}

func (c *StoreClient) parseJWS(jwsEncode string, claims jwt.Claims) error {
	rootCertBytes, err := c.cert.extractCertByIndex(jwsEncode, 2)
	if err != nil {
		return err
	}
	rootCert, err := x509.ParseCertificate(rootCertBytes)
	if err != nil {
		return fmt.Errorf("appstore failed to parse root certificate")
	}

	intermediaCertBytes, err := c.cert.extractCertByIndex(jwsEncode, 1)
	if err != nil {
		return err
	}
	intermediaCert, err := x509.ParseCertificate(intermediaCertBytes)
	if err != nil {
		return fmt.Errorf("appstore failed to parse intermediate certificate")
	}

	leafCertBytes, err := c.cert.extractCertByIndex(jwsEncode, 0)
	if err != nil {
		return err
	}
	leafCert, err := x509.ParseCertificate(leafCertBytes)
	if err != nil {
		return fmt.Errorf("appstore failed to parse leaf certificate")
	}
	if err = c.cert.verifyCert(rootCert, intermediaCert, leafCert); err != nil {
		return err
	}

	pk, ok := leafCert.PublicKey.(*ecdsa.PublicKey)
	if !ok {
		return fmt.Errorf("appstore public key must be of type ecdsa.PublicKey")
	}

	_, err = jwt.ParseWithClaims(jwsEncode, claims, func(token *jwt.Token) (interface{}, error) {
		return pk, nil
	})
	return err
}

func (c *StoreClient) ParseSignedTransaction(transaction string) (*JWSTransaction, error) {
	tran := &JWSTransaction{}
	err := c.parseJWS(transaction, tran)
	if err != nil {
		return nil, err
	}
	return tran, nil
}

// Per doc: https://developer.apple.com/documentation/appstoreserverapi#topics
func (c *StoreClient) Do(ctx context.Context, method string, url string, body io.Reader) (int, []byte, error) {
	req, err := http.NewRequest(method, url, body)
	if err != nil {
		return 0, nil, fmt.Errorf("appstore new http request err %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(ctx)
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("appstore http client do err %w", err)
	}
	defer resp.Body.Close()
	byteData, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, nil, fmt.Errorf("appstore read http body err %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		if rErr, ok := newAppStoreAPIError(byteData, resp.Header); ok {
			return resp.StatusCode, byteData, rErr
		}
	}
	return resp.StatusCode, byteData, err
}
