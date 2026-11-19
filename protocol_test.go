package tun

import (
	"bytes"
	"encoding/json/v2"
	"errors"
	"io"
	"strconv"
	"testing"
	"testing/iotest"

	"github.com/croaky/is"
)

func TestReadBody(t *testing.T) {
	for _, n := range []int{0, 1, 511, 512, 513, 4096, 70000, 200000} {
		data := bytes.Repeat([]byte("abc"), n/3+1)[:n]
		for _, size := range []int64{-1, 0, int64(n) / 2, int64(n), int64(n) + 10, MaxBody * 4} {
			t.Run(strconv.Itoa(n)+"/"+strconv.FormatInt(size, 10), func(t *testing.T) {
				is := is.New(t)
				// OneByteReader makes ReadBody grow its buffer many times.
				got, err := ReadBody(iotest.OneByteReader(bytes.NewReader(data)), size, 1024)
				is.NoErr(err)
				is.True(bytes.Equal(got, data))
			})
		}
	}
}

func TestReadBodyAllocatesOnce(t *testing.T) {
	data := make([]byte, 70000)
	got, err := ReadBody(bytes.NewReader(data), int64(len(data)), len(data))
	is.New(t).NoErr(err)
	is.New(t).Eq(cap(got), len(data)+1)
}

func TestReadBodyError(t *testing.T) {
	is := is.New(t)
	want := errors.New("boom")
	r := io.MultiReader(bytes.NewReader([]byte("ab")), iotest.ErrReader(want))

	got, err := ReadBody(r, -1, 1024)

	is.True(errors.Is(err, want))
	is.Eq(string(got), "ab")
}

// TestWireInvalidUTF8 checks that WireOptions changes each byte that
// is not UTF-8 to U+FFFD, as encoding/json v1 did, on Marshal and on
// Unmarshal.
func TestWireInvalidUTF8(t *testing.T) {
	is := is.New(t)

	msg, err := json.Marshal(Request{Headers: map[string][]string{"X-Obs": {"a\xffb"}}}, WireOptions)
	is.NoErr(err)
	var req Request
	is.NoErr(json.Unmarshal(msg, &req, WireOptions))
	is.Eq(req.Headers["X-Obs"], []string{"a\uFFFDb"})

	var resp Response
	is.NoErr(json.Unmarshal([]byte("{\"headers\":{\"X-Obs\":[\"c\xffd\"]}}"), &resp, WireOptions))
	is.Eq(resp.Headers["X-Obs"], []string{"c\uFFFDd"})
}

// TestWireNil checks the frames for a nil map and a nil body. v2 sends
// {} and "", and v1 sent null. A v1 peer decodes {} and "", and each
// side decodes null, so a client and a server on different versions
// still work together.
func TestWireNil(t *testing.T) {
	is := is.New(t)

	msg, err := json.Marshal(Response{ID: "1", Status: 200}, WireOptions)
	is.NoErr(err)
	is.Eq(string(msg), `{"id":"1","status":200,"headers":{},"body":""}`)

	for _, in := range []string{
		`{"id":"1","status":200,"headers":null,"body":null}`,
		`{"id":"1","status":200,"headers":{},"body":""}`,
	} {
		var resp Response
		is.NoErr(json.Unmarshal([]byte(in), &resp, WireOptions))
		is.Eq(resp.ID, "1")
		is.Eq(len(resp.Headers), 0)
		is.Eq(len(resp.Body), 0)

		var req Request
		is.NoErr(json.Unmarshal([]byte(in), &req, WireOptions))
		is.Eq(len(req.Headers), 0)
		is.Eq(len(req.Body), 0)
	}
}
