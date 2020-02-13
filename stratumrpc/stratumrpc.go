// Package stratumrpc implements a JSON-RPC Stratum ClientCodec and ServerCodec for the rpc2 package.
//
// Beside struct types, StratumCodec allows using positional arguments.
// Use []interface{} as the type of argument when sending and receiving methods.
//
// Positional arguments example:
// 	server.Handle("add", func(client *rpc2.Client, args []interface{}, result *float64) error {
// 		*result = args[0].(float64) + args[1].(float64)
// 		return nil
// 	})
//
//	var result float64
// 	client.Call("add", []interface{}{1, 2}, &result)
//
package stratumrpc

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"

	rpc2 "github.com/miningmeter/rpc2"
)

type stratumCodec struct {
	dec *json.Decoder // for reading JSON values
	enc *json.Encoder // for writing JSON values
	c   io.Closer

	// temporary work space
	msg            message
	serverRequest  serverRequest
	clientResponse clientResponse

	// JSON-RPC clients can use arbitrary json values as request IDs.
	// Package rpc expects uint64 request IDs.
	// We assign uint64 sequence numbers to incoming requests
	// but save the original request ID in the pending map.
	// When rpc responds, we use the sequence number in
	// the response to find the original request ID.
	mutex   sync.Mutex // protects seq, pending
	pending map[uint64]*json.RawMessage
	seq     uint64
}

// NewStratumCodec returns a new rpc2.Codec using JSON-RPC on conn.
func NewStratumCodec(conn io.ReadWriteCloser) rpc2.Codec {
	return &stratumCodec{
		dec:     json.NewDecoder(conn),
		enc:     json.NewEncoder(conn),
		c:       conn,
		pending: make(map[uint64]*json.RawMessage),
	}
}

// serverRequest and clientResponse combined
type message struct {
	Method string           `json:"method"`
	Params *json.RawMessage `json:"params"`
	ID     *json.RawMessage `json:"id"`
	Result *json.RawMessage `json:"result"`
	Error  interface{}      `json:"error"`
}

// Unmarshal to
type serverRequest struct {
	Method string           `json:"method"`
	Params *json.RawMessage `json:"params"`
	ID     *json.RawMessage `json:"id"`
}
type clientResponse struct {
	ID     uint64           `json:"id"`
	Result *json.RawMessage `json:"result"`
	Error  interface{}      `json:"error"`
}

// to Marshal
type serverResponse struct {
	ID     *json.RawMessage `json:"id"`
	Result interface{}      `json:"result"`
	Error  interface{}      `json:"error"`
}
type clientRequest struct {
	Method string        `json:"method"`
	Params []interface{} `json:"params"`
	ID     *uint64       `json:"id"`
}

func (c *stratumCodec) ReadHeader(req *rpc2.Request, resp *rpc2.Response) error {
	c.msg = message{}
	if err := c.dec.Decode(&c.msg); err != nil {
		return err
	}

	if c.msg.Method != "" {
		// request comes to server
		c.serverRequest.ID = c.msg.ID
		c.serverRequest.Method = c.msg.Method
		c.serverRequest.Params = c.msg.Params

		req.Method = c.serverRequest.Method

		// JSON request id can be any JSON value;
		// RPC package expects uint64.  Translate to
		// internal uint64 and save JSON on the side.
		if c.serverRequest.ID == nil {
			// Notification
		} else {
			c.mutex.Lock()
			c.seq++
			c.pending[c.seq] = c.serverRequest.ID
			c.serverRequest.ID = nil
			req.Seq = c.seq
			c.mutex.Unlock()
		}
	} else {
		// response comes to client
		err := json.Unmarshal(*c.msg.ID, &c.clientResponse.ID)
		if err != nil {
			return err
		}
		c.clientResponse.Result = c.msg.Result
		c.clientResponse.Error = c.msg.Error

		resp.Error = ""
		resp.Seq = c.clientResponse.ID
		if c.clientResponse.Error != nil {
			x, ok := c.clientResponse.Error.(string)
			if !ok {
				// Mining errors.
				a, ok := c.clientResponse.Error.([]interface{})
				if !ok {
					return fmt.Errorf("invalid error %v", c.clientResponse.Error)
				}
				x = a[1].(string)
			}
			if x == "" {
				x = "unspecified error"
			}
			resp.Error = x
		}
	}
	return nil
}

var errMissingParams = errors.New("sttratumrpc: request body missing params")

func (c *stratumCodec) ReadRequestBody(x interface{}) error {
	if x == nil {
		return nil
	}
	if c.serverRequest.Params == nil {
		return errMissingParams
	}
	var params *[]interface{}
	switch x := x.(type) {
	case *[]interface{}:
		params = x
	default:
		params = &[]interface{}{x}
	}
	return json.Unmarshal(*c.serverRequest.Params, params)
}

func (c *stratumCodec) ReadResponseBody(x interface{}) error {
	if x == nil {
		return nil
	}
	if c.clientResponse.Result == nil {
		x = c.clientResponse.Result
		return nil
	}
	return json.Unmarshal(*c.clientResponse.Result, x)
}

func (c *stratumCodec) WriteRequest(r *rpc2.Request, param interface{}) error {
	req := &clientRequest{Method: r.Method}
	switch param := param.(type) {
	case []interface{}:
		req.Params = param
	default:
		req.Params = []interface{}{param}
	}
	if r.Seq == 0 {
		// Notification
		req.ID = nil
	} else {
		seq := r.Seq
		req.ID = &seq
	}
	return c.enc.Encode(req)
}

var null = json.RawMessage([]byte("null"))

func (c *stratumCodec) WriteResponse(r *rpc2.Response, x interface{}) error {
	var iErr []interface{}

	c.mutex.Lock()
	b, ok := c.pending[r.Seq]
	if !ok {
		c.mutex.Unlock()
		return errors.New("invalid sequence number in response")
	}
	delete(c.pending, r.Seq)
	c.mutex.Unlock()

	if b == nil {
		// Invalid request so no id.  Use JSON null.
		b = &null
	}
	resp := serverResponse{ID: b}
	if r.Error == "" {
		resp.Result = x
	} else {
		err := json.Unmarshal([]byte(r.Error), &iErr)
		if err == nil {
			resp.Error = iErr
		} else {
			resp.Error = r.Error
		}
	}
	return c.enc.Encode(resp)
}

func (c *stratumCodec) Close() error {
	return c.c.Close()
}
