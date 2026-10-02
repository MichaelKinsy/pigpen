// Generated from types/*.ts — do not edit.
//
// Regenerate with: npm run generate:go

package ahptypes

import (
	"encoding/json"
	"fmt"
)

// ─── JSON-RPC Envelope ────────────────────────────────────────────────────

// JsonRpcVersion is the sole allowed value of the `jsonrpc` field
// (`"2.0"`).
type JsonRpcVersion string

// JsonRpcV2 is the canonical `"2.0"` JSON-RPC version literal.
const JsonRpcV2 JsonRpcVersion = "2.0"

// JsonRpcRequest is a JSON-RPC 2.0 request (method + id).
type JsonRpcRequest struct {
	JsonRpc JsonRpcVersion  `json:"jsonrpc"`
	ID      uint64          `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// JsonRpcSuccessResponse is a JSON-RPC 2.0 success response.
type JsonRpcSuccessResponse struct {
	JsonRpc JsonRpcVersion  `json:"jsonrpc"`
	ID      uint64          `json:"id"`
	Result  json.RawMessage `json:"result"`
}

// JsonRpcErrorResponse is a JSON-RPC 2.0 error response.
type JsonRpcErrorResponse struct {
	JsonRpc JsonRpcVersion `json:"jsonrpc"`
	ID      uint64         `json:"id"`
	Error   JsonRpcError   `json:"error"`
}

// JsonRpcError is the standard JSON-RPC 2.0 error object.
type JsonRpcError struct {
	Code    int32           `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

// Error implements the standard error interface.
func (e *JsonRpcError) Error() string {
	return fmt.Sprintf("jsonrpc error %d: %s", e.Code, e.Message)
}

// JsonRpcNotification is a JSON-RPC 2.0 notification (method, no id).
type JsonRpcNotification struct {
	JsonRpc JsonRpcVersion  `json:"jsonrpc"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// JsonRpcMessage is a discriminated union over the four JSON-RPC
// message shapes. Use [DecodeJsonRpcMessage] to parse an inbound frame
// into the correct variant.
type JsonRpcMessage struct {
	Request         *JsonRpcRequest
	SuccessResponse *JsonRpcSuccessResponse
	ErrorResponse   *JsonRpcErrorResponse
	Notification    *JsonRpcNotification
}

// MarshalJSON encodes whichever variant is populated.
func (m JsonRpcMessage) MarshalJSON() ([]byte, error) {
	switch {
	case m.Request != nil:
		return json.Marshal(m.Request)
	case m.SuccessResponse != nil:
		return json.Marshal(m.SuccessResponse)
	case m.ErrorResponse != nil:
		return json.Marshal(m.ErrorResponse)
	case m.Notification != nil:
		return json.Marshal(m.Notification)
	default:
		return []byte("null"), nil
	}
}

// UnmarshalJSON inspects the raw object's shape to pick a variant.
//
// JSON-RPC 2.0's shape rules:
//   - request:        has `id` and `method`
//   - notification:   has `method` but no `id`
//   - success-resp:   has `id` and `result` (no `error`)
//   - error-resp:     has `id` and `error` (no `result`)
func (m *JsonRpcMessage) UnmarshalJSON(data []byte) error {
	*m = JsonRpcMessage{}
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(data, &probe); err != nil {
		return err
	}
	_, hasMethod := probe["method"]
	_, hasID := probe["id"]
	_, hasResult := probe["result"]
	_, hasError := probe["error"]
	switch {
	case hasMethod && hasID:
		var v JsonRpcRequest
		if err := json.Unmarshal(data, &v); err != nil {
			return err
		}
		m.Request = &v
	case hasMethod:
		var v JsonRpcNotification
		if err := json.Unmarshal(data, &v); err != nil {
			return err
		}
		m.Notification = &v
	case hasError:
		var v JsonRpcErrorResponse
		if err := json.Unmarshal(data, &v); err != nil {
			return err
		}
		m.ErrorResponse = &v
	case hasResult:
		var v JsonRpcSuccessResponse
		if err := json.Unmarshal(data, &v); err != nil {
			return err
		}
		m.SuccessResponse = &v
	default:
		return fmt.Errorf("ahptypes: JSON-RPC message has no method/result/error")
	}
	return nil
}

// ActionNotificationParams is the params shape of the server → client
// `action` JSON-RPC method.
type ActionNotificationParams = ActionEnvelope
