package tun

import (
	"bytes"
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
