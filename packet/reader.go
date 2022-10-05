package packet

import (
	"errors"
	"fmt"
	"io"
	"math"
)

type Reader struct {
	buf []byte

	at int
}

func NewReader(b []byte) *Reader {
	return &Reader{buf: b}
}

func (r *Reader) Bool(x *bool) error {
	if r.isEOF(1) {
		return io.ErrUnexpectedEOF
	}

	b := r.buf[r.at]
	r.at++
	if b > 1 {
		return fmt.Errorf("boolean overflows a 1-bit integer")
	}

	*x = (b == 0x01)
	return nil
}

func (r *Reader) Uint8(x *uint8) error {
	if r.isEOF(1) {
		return io.ErrUnexpectedEOF
	}

	*x = r.buf[r.at]
	r.at++
	return nil
}

func (r *Reader) Int8(x *int8) error {
	if r.isEOF(1) {
		return io.ErrUnexpectedEOF
	}

	*x = int8(r.buf[r.at])
	r.at++
	return nil
}

func (r *Reader) Uint16(x *uint16) error {
	if r.isEOF(2) {
		return io.ErrUnexpectedEOF
	}

	b := r.buf[r.at : r.at+2]
	r.at += 2

	*x = uint16(b[0])<<8 | uint16(b[1])
	return nil
}

func (r *Reader) Int16(x *int16) error {
	if r.isEOF(2) {
		return io.ErrUnexpectedEOF
	}

	b := r.buf[r.at : r.at+2]
	r.at += 2

	*x = int16(uint16(b[0])<<8 | uint16(b[1]))
	return nil
}

func (r *Reader) Int32(x *int32) error {
	if r.isEOF(4) {
		return io.ErrUnexpectedEOF
	}

	b := r.buf[r.at : r.at+4]
	r.at += 4

	*x = int32(b[0])<<24 | int32(b[1])<<16 | int32(b[2])<<8 | int32(b[3])
	return nil
}

func (r *Reader) Int64(x *int64) error {
	if r.isEOF(8) {
		return io.ErrUnexpectedEOF
	}

	b := r.buf[r.at : r.at+8]
	r.at += 8

	*x = int64(b[0])<<56 | int64(b[1])<<48 | int64(b[2])<<40 | int64(b[3])<<32 | int64(b[4])<<24 | int64(b[5])<<16 | int64(b[6])<<8 | int64(b[7])
	return nil
}

func (r *Reader) Float32(x *float32) error {
	if r.isEOF(4) {
		return io.ErrUnexpectedEOF
	}

	b := r.buf[r.at : r.at+4]
	r.at += 4

	ub := uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])

	*x = math.Float32frombits(ub)
	return nil
}

func (r *Reader) Float64(x *float64) error {
	if r.isEOF(8) {
		return io.ErrUnexpectedEOF
	}

	b := r.buf[r.at : r.at+8]
	r.at += 8

	ub := uint64(b[0])<<56 | uint64(b[1])<<48 | uint64(b[2])<<40 | uint64(b[3])<<32 | uint64(b[4])<<24 | uint64(b[5])<<16 | uint64(b[6])<<8 | uint64(b[7])

	*x = math.Float64frombits(ub)
	return nil
}

var overflow = errors.New("var-int overflows a 32-bit integer")

func (r *Reader) VarInt(x *int32) error {
	var ux uint32
	for i := uint32(0); i < 35; i += 7 {
		if r.isEOF(1) {
			return io.ErrUnexpectedEOF
		}

		b := r.buf[r.at]
		r.at++

		ux |= uint32(b&0x7F) << i

		if b < 0x80 {
			*x = int32(ux)
			return nil
		}
	}

	return overflow
}

func (r *Reader) String(x *string) error {
	var length int32
	if err := r.VarInt(&length); err != nil {
		return err
	}

	if r.isEOF(int(length)) {
		return io.ErrUnexpectedEOF
	}

	b := r.buf[r.at : r.at+int(length)]
	r.at += int(length)

	*x = string(b)
	return nil
}

func (r *Reader) ByteArray(x *[]byte) error {
	var length int32
	if err := r.VarInt(&length); err != nil {
		return err
	}

	if r.isEOF(int(length)) {
		return io.ErrUnexpectedEOF
	}

	b := r.buf[r.at : r.at+int(length)]
	r.at += int(length)

	*x = b
	return nil
}
func (r *Reader) UUID(x *[16]byte) error {
	if r.isEOF(16) {
		return io.ErrUnexpectedEOF
	}

	r.at += copy((*x)[:], r.buf[r.at:r.at+16])
	return nil
}

func (r *Reader) isEOF(n int) bool {
	return r.at+n > len(r.buf)
}
