package main

import (
	"bufio"
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"strconv"
	"strings"
	"time"
)

const maxMessageSize uint32 = 1 << 30 // 1GB (2^30 bytes)
var serverDirectory *string            // Global var. Directory to serve files from
var statusLines map[int]string        // Global var. Map to store status lines

func init() {
	// Init status lines
	statusLines = make(map[int]string)
	statusLines[501] = "HTTP/1.1 501 Not Implemented"
	statusLines[500] = "HTTP/1.1 500 Internal Server Error"
	statusLines[404] = "HTTP/1.1 404 Not Found"
	statusLines[400] = "HTTP/1.1 400 Bad Request"
	statusLines[200] = "HTTP/1.1 200 OK"
	statusLines[201] = "HTTP/1.1 201 Created"
}

func main() {
	serverDirectory = flag.String("directory", "", "Directory to serve files from")
	flag.Parse()

	fmt.Println("Serving files from directory:", serverDirectory)

	os.Exit(listen())
}

// Get statusLines value with safe error handling for key errors.
// Single CRLR at end.
func getStatusLine(code int) (statusLine string) {
	statusLine, ok := statusLines[code]
	if ok {
		return statusLine + "\r\n"
	}

	// Default value when there is a key error
	return "HTTP/1.1 500 Internal Server Error\r\n"
}

func listen() (exit_code int) {
	l, err := net.Listen("tcp", "0.0.0.0:4221")
	if err != nil {
		fmt.Println("Failed to bind to port 4221")
		return 1
	}

	for {
		conn, err := l.Accept()
		if err != nil {
			fmt.Println("Error accepting connection: ", err.Error())
			return 1
		}

		go handleConnection(conn)
	}
}

func handleConnection(conn net.Conn) {
	fmt.Println("Handling new connection")
	defer conn.Close()
	// start listening
	message, err := readFromConnection(conn)
	if err != nil {
		fmt.Println("There was an error reading from connection. Closing.", err.Error())
		return
	}

	req, err := parseHTTPRequest(message)

	if err != nil {
		fmt.Println("There was an error prasing the HTTPRequest. Discarding.")
		return
	}

	var response []byte

	switch req.Method {
	case "GET":
		response, err = handleGETRequest(req)
	case "POST":
		response, err = handlePOSTRequest(req)
	default:
		{
			fmt.Println("Unsupported HTTP method:", req.Method)
			response = []byte(getStatusLine(501) + "\r\n")
		}
	}

	if err != nil {
		fmt.Println("Unhandled error while generating response:", err.Error())
	}

	writeToConnection(conn, response)
}

// Helper function to handle requests that are universal between all request methods
//
// Parameters:
//
//	req - The filled out httpRequest object to generate a response from
//
// Returns:
//
//	response - The byte slice response if handled. nil otherwise.
//	handled - Whenever or not the function was able to handle the request.
func handleUniversalTargets(req httpRequest) (response []byte, handled bool) {
	var err error
	defer func() {
		if err != nil {
			response, handled = nil, false
			fmt.Println("There was an error when trying to handle this request:", err.Error())
		}
	}()

	if req.Target == "/" {
		// Universal ping back. No need to do anything
		response, err = (&httpResponse{ResponseCode: 200}).ByteResponse(true)
	} else {
		return nil, false // Be explicit for clarity
	}

	return response, true
}

// Helper function to handle POST specific requests.
//
// Parameters:
//
//	req - The filled out httpRequest object to generate a response from
//
// Returns:
//
//	response - The byte slice response. nil if fatal errored
//	err - The error if there were any. nil if no errors
func handlePOSTRequest(req httpRequest) (response []byte, err error) {
	if response, handled := handleUniversalTargets(req); handled {
		return response, nil
	}

	res := httpResponse{}

	targetParts := strings.SplitN(req.Target, "/", 3)
	if len(targetParts) < 2 {
		res.ResponseCode = 400
		res.Body = []byte("Invalid target structure\n")
		return res.ByteResponse(true)
	}

	switch targetParts[1] {
	case "files":
		{
			if len(targetParts) < 3 {
				res.ResponseCode = 400
				res.Body = []byte("Invalid files target path\n")
				return res.ByteResponse(true)
			}

			path := *serverDirectory + targetParts[2]
			err = os.WriteFile(path, []byte(req.Body), 0755)
			if err != nil {
				fmt.Printf("Error writing file %s: %s", path, err.Error())
				res.ResponseCode = 500
				return res.ByteResponse(true)
			}

			res.ResponseCode = 201
		}
	default:
		{
			res.ResponseCode = 400
			res.Body = []byte("Invalid target")
		}
	}
	return res.ByteResponse(true)
}

// Helper function to handle GET specific requests.
//
// Parameters:
//
//	req - The filled out httpRequest object to generate a response from
//
// Returns:
//
//	response - The byte slice response. nil if errored
//	err - The error if there were any. nil if no errors
func handleGETRequest(req httpRequest) (response []byte, err error) {
	if response, handled := handleUniversalTargets(req); handled {
		return response, nil
	}

	res := httpResponse{
		EncodingMethod: req.AcceptEncoding,
	}

	targetParts := strings.SplitN(req.Target, "/", 3)
	if len(targetParts) < 2 {
		res.ResponseCode = 400
		res.Body = []byte("Invalid target structure")
		return res.ByteResponse(true)
	}
	switch targetParts[1] {
	case "echo":
		{
			res.ResponseCode, res.Body, res.ContentType = 200, []byte(targetParts[2]), "text/plain"
		}
	case "user-agent":
		{
			res.ResponseCode, res.Body, res.ContentType = 200, []byte(req.UserAgent), "text/plain"
		}
	case "files":
		{
			// Try to get and return the requested file
			if len(targetParts) < 3 {
				res.ResponseCode = 400
				res.Body = []byte("Invalid file target structure\n")
				return res.ByteResponse(true)
			}
			fileRelPath := targetParts[2]
			_, err = os.Stat(*serverDirectory + fileRelPath)

			if errors.Is(err, fs.ErrNotExist) {
				res.ResponseCode = 404
				return res.ByteResponse(true)
			} else if err != nil {
				fmt.Println("Error getting file:", err.Error())
				res.ResponseCode = 500
				return res.ByteResponse(true)
			} else {
				file, err := os.ReadFile(*serverDirectory + fileRelPath)
				if err != nil {
					fmt.Println("Error reading file:", err.Error())
					res.ResponseCode = 500
					res.Body = []byte("There was an error reading the requested file\n")
					return res.ByteResponse(true)
				}
				res.ResponseCode, res.Body, res.ContentType = 200, file, "application/octet-stream"
			}
		}
	default:
		res.ResponseCode = 404
	}

	return res.ByteResponse(true)
}

// Helper function to write to the connection
func writeToConnection(conn net.Conn, output []byte) bool {
	if _, err := conn.Write(output); err != nil {
		fmt.Println("Error writing to connection:", err.Error())
		return false
	}
	return true
}

func readFromConnection(conn net.Conn) (fullMessage []byte, err error) {
	buffer := make([]byte, 1024)
	conn.SetReadDeadline(time.Now().Add(20 * time.Second))

	for {
		n, err := conn.Read(buffer)

		if err != nil {
			return nil, fmt.Errorf("Error reading from connection: %w", err)
		}

		fullMessage = append(fullMessage, buffer[:n]...)

		if len(fullMessage) > int(maxMessageSize) {
			return nil, fmt.Errorf("message from connection exceeded limit of %d bytes", maxMessageSize)
		}

		if n < len(buffer) {
			// Completed reading
			break
		}
	}

	return fullMessage, nil
}

func parseHTTPRequest(message []byte) (req httpRequest, err error) {
	reader := bufio.NewReader(bytes.NewReader(message))
	// Line
	line, err := readLine(reader)
	if err != nil {
		fmt.Println("Error prasing request:", err.Error())
		return
	}
	req.Line = line
	parts := strings.SplitN(line, " ", 3)

	if len(parts) < 3 {
		fmt.Println("Error parsing line. Missing part")
		return req, fmt.Errorf("missing part when parsing line")
	}

	req.Method = parts[0]
	req.Target = parts[1]
	req.HTTPVersion = parts[2]

	// Headers
	for {
		line, err = readLine(reader)
		if err != nil {
			fmt.Println("Error parsing header part:", err.Error())
			return
		}
		if line == "" {
			break // Finished parsing header
		}

		parts := strings.SplitN(line, ": ", 2)

		switch strings.ToLower(parts[0]) {
		case "host":
			req.Host = parts[1]
		case "user-agent":
			req.UserAgent = parts[1]
		case "accept":
			req.Accept = parts[1]
		case "content-type":
			req.ContentType = parts[1]
		case "content-length":
			{
				num, err := strconv.Atoi(parts[1])
				if err != nil {
					fmt.Println("Error parsing content-length", parts[1])
					req.ContentLength = -1
				} else {
					req.ContentLength = num
				}
			}
		case "accept-encoding":
			req.AcceptEncoding = parts[1]

		default:
			fmt.Println("Error parsing header part. Unknown label:", parts[0])
		}
	}

	if req.ContentLength != -1 {
		buff := make([]byte, req.ContentLength)
		_, err = io.ReadFull(reader, buff)
		if err != nil {
			fmt.Println("Error filling buffer from body")
			return
		}
		req.Body = buff

	} else {
		// Body if it exists
		var body string
		body, err = readLine(reader)
		if err == nil {
			req.Body = []byte(body)
		} else if err.Error() != "EOF" {
			fmt.Println("Error trying to parse body:", err.Error())
			return
		}
	}

	return req, nil
}

func readLine(reader *bufio.Reader) (lineStr string, err error) {
	var line []byte
	for {
		part, isPrefix, err := reader.ReadLine()
		if err != nil {
			return "", err
		}
		line = append(line, part...)
		if !isPrefix {
			return string(line), nil
		}
	}
}

type httpRequest struct {
	Line           string
	Body           []byte
	Method         string
	Target         string
	HTTPVersion    string
	Host           string // Server host and port
	UserAgent      string // Client user agent
	Accept         string // Media types the client accepts
	ContentType    string
	ContentLength  int
	AcceptEncoding string
}

type httpResponse struct {
	ResponseCode   int
	ContentType    string
	EncodingMethod string
	Encoded        bool
	Body           []byte
}

type UnsupportedEncodingError struct {
	Method string
}

func (e *UnsupportedEncodingError) Error() string {
	return fmt.Sprintf("tried to use unsupported encoding method: %s", e.Method)
}

type TargetParseError struct {
	Details string
}

func (e *TargetParseError) Error() string {
	return fmt.Sprintf("error parsing request target: %s", e.Details)
}

// Converts the httpResponse object into a byte slice.
// The created response body will be encoded based on the request.
//
// Parameters:
//
//	mutate -  A boolean indicating whether it is okay for the resp object to be mutated. Costs more memory and more computationally expensive.
//
// Returns:
//
//	byteResponse - A byte slice representing the encoded httpResponse object. nil if errored
//	err - An error if the conversion fails, nil otherwise
func (resp *httpResponse) ByteResponse(mutate bool) (byteResponse []byte, err error) {
	if mutate {
		err = resp.Encode()
	} else {
		resp, err = resp.SafeEncode()
	}

	if err != nil {
		switch err.(type) {
		case *UnsupportedEncodingError:
			fmt.Println("Non fatal error while encoding:", err.Error())
		default:
			fmt.Println("Fatal uncaught error while encoding:", err.Error())
			return nil, err
		}
	}

	byteResponse = []byte(getStatusLine(resp.ResponseCode))
	headers := map[string]string{}
	// Body based headers
	if resp.Body != nil {
		if resp.Encoded {
			headers["Content-Encoding"] = resp.EncodingMethod
		}
		if resp.ContentType != "" {
			headers["Content-Type"] = resp.ContentType
		}
		headers["Content-Length"] = fmt.Sprintf("%d", resp.GetContentLength())
	}

	for key, value := range headers {
		byteResponse = appendStr(byteResponse, fmt.Sprintf("%s: %s\r\n", key, value))
	}

	byteResponse = appendStr(byteResponse, "\r\n") // Append CRLR
	byteResponse = append(byteResponse, resp.Body...)

	return byteResponse, err
}

// Go routine safe helper function to encode the data based on the requested method.
// If the requested encoding method is not supported, will reset the method to an empty string.
// Does no action if the body is nil.
// Returns a new response object and does not mutate to ensure thread safety.
func (resp httpResponse) SafeEncode() (newRespObject *httpResponse, err error) {
	// Perform deep copy
	newRespObject = &httpResponse{
		ResponseCode:   resp.ResponseCode,
		ContentType:    resp.ContentType,
		EncodingMethod: resp.EncodingMethod,
		Encoded:        resp.Encoded,
		Body:           make([]byte, len(resp.Body)),
	}

	copy(newRespObject.Body, resp.Body)
	err = newRespObject.Encode()
	return newRespObject, err
}

// Mutating helper function to encode the response data based on the requested method.
// If the requested encoding method is not supported, will reset the method to an empty string.
// Does no action if the body is nil.
// Returns the error if encoding failed, nil otherwise
func (resp *httpResponse) Encode() (err error) {
	if resp.Body == nil || resp.EncodingMethod == ""{
		// Nothing to encode. Return
		return err
	}

	switch resp.EncodingMethod {
	case "gzip":
		break
	default:
		{
			err = &UnsupportedEncodingError{Method: resp.EncodingMethod}
			resp.EncodingMethod = ""
			resp.Encoded = false
			return err
		}
	}

	resp.Encoded = true
	return nil
}

// Get the content length based on the size of the body of resp in bytes.
// Returns 0 if the body is nil
func (resp httpResponse) GetContentLength() int {
	if resp.Body == nil {
		return 0
	}
	return len(resp.Body)
}

// Helper function to append a string to a byte slice
func appendStr(slice []byte, str string) []byte {
	return append(slice, []byte(str)...)
}
