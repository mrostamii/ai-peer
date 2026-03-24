package x402client

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/mrostamii/ai-peer/pkg/x402spike"
)

type Client struct {
	HTTPClient  *http.Client
	PrivateKey  string
	NowProvider func() time.Time
}

func NewFromEnv() (*Client, error) {
	key := strings.TrimSpace(os.Getenv("EVM_PRIVATE_KEY"))
	if key == "" {
		return nil, errors.New("EVM_PRIVATE_KEY is empty")
	}
	return &Client{
		HTTPClient:  http.DefaultClient,
		PrivateKey:  key,
		NowProvider: time.Now,
	}, nil
}

func (c *Client) DoWithPayment(req *http.Request) (*http.Response, error) {
	if req == nil {
		return nil, errors.New("request is nil")
	}
	if c == nil {
		return nil, errors.New("client is nil")
	}
	if strings.TrimSpace(c.PrivateKey) == "" {
		return nil, errors.New("private key is empty")
	}
	httpClient := c.HTTPClient
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	nowFn := c.NowProvider
	if nowFn == nil {
		nowFn = time.Now
	}

	body, err := requestBodyBytes(req)
	if err != nil {
		return nil, fmt.Errorf("read request body: %w", err)
	}

	firstReq, err := cloneRequest(req, body)
	if err != nil {
		return nil, err
	}
	firstResp, err := httpClient.Do(firstReq)
	if err != nil {
		return nil, err
	}
	if firstResp.StatusCode != http.StatusPaymentRequired {
		return firstResp, nil
	}

	paymentRequiredHeader := firstResp.Header.Get("PAYMENT-REQUIRED")
	if strings.TrimSpace(paymentRequiredHeader) == "" {
		return firstResp, nil
	}

	var paymentRequired x402spike.PaymentRequired
	if err := x402spike.DecodeBase64JSON(paymentRequiredHeader, &paymentRequired); err != nil {
		firstResp.Body.Close()
		return nil, fmt.Errorf("decode PAYMENT-REQUIRED: %w", err)
	}
	if len(paymentRequired.Accepts) == 0 {
		firstResp.Body.Close()
		return nil, errors.New("PAYMENT-REQUIRED accepts list is empty")
	}

	payload, err := x402spike.BuildPaymentPayload(
		c.PrivateKey,
		paymentRequired.Accepts[0],
		paymentRequired.Resource,
		nowFn(),
	)
	if err != nil {
		firstResp.Body.Close()
		return nil, fmt.Errorf("build payment payload: %w", err)
	}
	paymentSignature, err := x402spike.EncodeBase64JSON(payload)
	if err != nil {
		firstResp.Body.Close()
		return nil, fmt.Errorf("encode payment signature: %w", err)
	}

	_ = firstResp.Body.Close()
	secondReq, err := cloneRequest(req, body)
	if err != nil {
		return nil, err
	}
	secondReq.Header.Set("PAYMENT-SIGNATURE", paymentSignature)
	return httpClient.Do(secondReq)
}

func requestBodyBytes(req *http.Request) ([]byte, error) {
	if req.Body == nil {
		return nil, nil
	}
	if req.GetBody != nil {
		r, err := req.GetBody()
		if err != nil {
			return nil, err
		}
		defer r.Close()
		return io.ReadAll(r)
	}
	raw, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}
	req.Body = io.NopCloser(bytes.NewReader(raw))
	return raw, nil
}

func cloneRequest(req *http.Request, body []byte) (*http.Request, error) {
	clone := req.Clone(req.Context())
	if body != nil {
		clone.Body = io.NopCloser(bytes.NewReader(body))
		clone.GetBody = func() (io.ReadCloser, error) {
			return io.NopCloser(bytes.NewReader(body)), nil
		}
		clone.ContentLength = int64(len(body))
	} else {
		clone.Body = nil
		clone.GetBody = nil
		clone.ContentLength = 0
	}
	return clone, nil
}
