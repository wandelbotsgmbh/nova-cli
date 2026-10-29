// Package sows implements the SOWS (Secure Omni WebSocket) binary protocol
// used by Nucleus Auth and Discovery services.
//
// Message format:
//
//	Header: [type:uint8][requestId:uint32-LE] = 5 bytes
//
// Client → Server:
//
//	Type 0: Cancel      - header only
//	Type 1: Request     - header + method\0 + paramsLen:uint32-LE + paramsJSON
//	Type 2: StreamReq   - same as type 1, signals binary data follows
//	Type 3: Continue    - header + binary data
//	Type 4: EndStream   - header + optional binary data
//
// Server → Client:
//
//	Type 0: Error       - header + code:uint16-LE + message:string
//	Type 1: Response    - header + last:uint8 + paramsLen:uint32-LE + paramsJSON [+ binary]
//	Type 2: StreamResp  - same as type 1
//	Type 3: ContResp    - header + binary data
//	Type 4: EndResp     - header + optional binary data
//	Type 5: Done        - header only
package sows

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"slices"
)

// Client → Server message types.
const (
	TypeCancel    = 0
	TypeRequest   = 1
	TypeStreamReq = 2
	TypeContinue  = 3
	TypeEndStream = 4
)

// Server → Client message types.
const (
	TypeError      = 0
	TypeResponse   = 1
	TypeStreamResp = 2
	TypeContResp   = 3
	TypeEndResp    = 4
	TypeDone       = 5
)

// Response is a parsed SOWS server response.
type Response struct {
	Type      byte
	RequestID uint32

	// For Error (type 0)
	ErrorCode    uint16
	ErrorMessage string

	// For Response (type 1/2)
	Last       bool
	ParamsJSON json.RawMessage
	Binary     []byte
}

// BuildRequest packs a SOWS type-1 (Request) message.
//
//	header + interfaceName.methodName\0 + paramsLen:uint32-LE + paramsJSON
func BuildRequest(requestID uint32, interfaceName, methodName string, params any) ([]byte, error) {
	paramsJSON, err := json.Marshal(params)
	if err != nil {
		return nil, fmt.Errorf("marshal params: %w", err)
	}

	method := interfaceName + "." + methodName
	methodBytes := append([]byte(method), 0) // null-terminated

	// Header: type(1) + requestId(4)
	header := make([]byte, 5)
	header[0] = TypeRequest
	binary.LittleEndian.PutUint32(header[1:5], requestID)

	// Params length
	lenBuf := make([]byte, 4)
	binary.LittleEndian.PutUint32(lenBuf, uint32(len(paramsJSON)))

	// Assemble: header + method\0 + paramsLen + paramsJSON
	return slices.Concat(header, methodBytes, lenBuf, paramsJSON), nil
}

// BuildResponse packs a SOWS type-1 (Response) message.
// This is useful for building fake server responses in tests.
func BuildResponse(requestID uint32, last bool, params any) ([]byte, error) {
	paramsJSON, err := json.Marshal(params)
	if err != nil {
		return nil, fmt.Errorf("marshal params: %w", err)
	}

	header := make([]byte, 10)
	header[0] = TypeResponse
	binary.LittleEndian.PutUint32(header[1:5], requestID)
	if last {
		header[5] = 1
	}
	binary.LittleEndian.PutUint32(header[6:10], uint32(len(paramsJSON)))
	return slices.Concat(header, paramsJSON), nil
}

// ParseResponse parses a SOWS response message from raw bytes.
func ParseResponse(data []byte) (*Response, error) {
	if len(data) < 5 {
		return nil, fmt.Errorf("sows response too short: %d bytes", len(data))
	}

	resp := &Response{
		Type:      data[0],
		RequestID: binary.LittleEndian.Uint32(data[1:5]),
	}

	switch resp.Type {
	case TypeError:
		if len(data) < 7 {
			return nil, fmt.Errorf("sows error response too short")
		}
		resp.ErrorCode = binary.LittleEndian.Uint16(data[5:7])
		resp.ErrorMessage = string(data[7:])

	case TypeResponse, TypeStreamResp:
		if len(data) < 10 {
			return nil, fmt.Errorf("sows response body too short")
		}
		// Header: last(1) + paramsLen(4), followed by params JSON and optional binary.
		hdr, body := data[5:10], data[10:]
		resp.Last = hdr[0] != 0
		paramsLen := uint64(binary.LittleEndian.Uint32(hdr[1:5]))
		if paramsLen > uint64(len(body)) {
			return nil, fmt.Errorf("sows params truncated: want %d, have %d", paramsLen, len(body))
		}
		resp.ParamsJSON = json.RawMessage(body[:paramsLen])
		if rest := body[paramsLen:]; len(rest) > 0 {
			resp.Binary = rest
		}

	case TypeContResp:
		if len(data) > 5 {
			resp.Binary = data[5:]
		}

	case TypeEndResp:
		if len(data) > 5 {
			resp.Binary = data[5:]
		}

	case TypeDone:
		// No extra data

	default:
		return nil, fmt.Errorf("unknown sows response type: %d", resp.Type)
	}

	return resp, nil
}
