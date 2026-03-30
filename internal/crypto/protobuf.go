// Package crypto provides lightweight cryptographic operations for Cosmos transactions
// protobuf.go implements minimal protobuf wire format encoding.
// This avoids depending on the full Cosmos SDK or google.golang.org/protobuf.
package crypto

import (
	"encoding/binary"
	"math"
)

// Protobuf wire types
const (
	wireVarint          = 0 // int32, int64, uint32, uint64, sint32, sint64, bool, enum
	wire64Bit           = 1 // fixed64, sfixed64, double
	wireLengthDelimited = 2 // string, bytes, embedded messages, packed repeated
	wire32Bit           = 5 // fixed32, sfixed32, float
)

// ProtoWriter accumulates protobuf-encoded fields.
type ProtoWriter struct {
	buf []byte
}

// NewProtoWriter creates a new ProtoWriter.
func NewProtoWriter() *ProtoWriter {
	return &ProtoWriter{}
}

// Bytes returns the accumulated protobuf bytes.
func (w *ProtoWriter) Bytes() []byte {
	return w.buf
}

// Len returns the current length.
func (w *ProtoWriter) Len() int {
	return len(w.buf)
}

// appendVarint appends a varint-encoded uint64.
func (w *ProtoWriter) appendVarint(v uint64) {
	var buf [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(buf[:], v)
	w.buf = append(w.buf, buf[:n]...)
}

// appendTag appends a protobuf field tag (field_number << 3 | wire_type).
func (w *ProtoWriter) appendTag(fieldNumber int, wireType int) {
	w.appendVarint(uint64(fieldNumber<<3 | wireType))
}

// WriteVarint writes a uint64 varint field.
func (w *ProtoWriter) WriteVarint(fieldNumber int, v uint64) {
	if v == 0 {
		return // proto3: default values are not serialized
	}
	w.appendTag(fieldNumber, wireVarint)
	w.appendVarint(v)
}

// WriteVarintForce writes a uint64 varint field even if zero (for SignDoc account_number).
func (w *ProtoWriter) WriteVarintForce(fieldNumber int, v uint64) {
	w.appendTag(fieldNumber, wireVarint)
	w.appendVarint(v)
}

// WriteSignedVarint writes a int64 varint field (standard encoding, not zigzag).
func (w *ProtoWriter) WriteSignedVarint(fieldNumber int, v int64) {
	if v == 0 {
		return
	}
	w.appendTag(fieldNumber, wireVarint)
	w.appendVarint(uint64(v))
}

// WriteBool writes a bool field.
func (w *ProtoWriter) WriteBool(fieldNumber int, v bool) {
	if !v {
		return
	}
	w.appendTag(fieldNumber, wireVarint)
	w.appendVarint(1)
}

// WriteBytes writes a bytes/string field.
func (w *ProtoWriter) WriteBytes(fieldNumber int, data []byte) {
	if len(data) == 0 {
		return // proto3: empty bytes/string not serialized
	}
	w.appendTag(fieldNumber, wireLengthDelimited)
	w.appendVarint(uint64(len(data)))
	w.buf = append(w.buf, data...)
}

// WriteString writes a string field.
func (w *ProtoWriter) WriteString(fieldNumber int, s string) {
	if s == "" {
		return // proto3: empty string not serialized
	}
	w.appendTag(fieldNumber, wireLengthDelimited)
	w.appendVarint(uint64(len(s)))
	w.buf = append(w.buf, s...)
}

// WriteMessage writes an embedded message field.
func (w *ProtoWriter) WriteMessage(fieldNumber int, data []byte) {
	if len(data) == 0 {
		return
	}
	w.appendTag(fieldNumber, wireLengthDelimited)
	w.appendVarint(uint64(len(data)))
	w.buf = append(w.buf, data...)
}

// WriteMessageAlways writes an embedded message even if empty (for required nested fields).
func (w *ProtoWriter) WriteMessageAlways(fieldNumber int, data []byte) {
	w.appendTag(fieldNumber, wireLengthDelimited)
	w.appendVarint(uint64(len(data)))
	w.buf = append(w.buf, data...)
}

// WriteFixed32 writes a fixed32 field.
func (w *ProtoWriter) WriteFixed32(fieldNumber int, v uint32) {
	if v == 0 {
		return
	}
	w.appendTag(fieldNumber, wire32Bit)
	var buf [4]byte
	binary.LittleEndian.PutUint32(buf[:], v)
	w.buf = append(w.buf, buf[:]...)
}

// WriteFixed64 writes a fixed64 field.
func (w *ProtoWriter) WriteFixed64(fieldNumber int, v uint64) {
	if v == 0 {
		return
	}
	w.appendTag(fieldNumber, wire64Bit)
	var buf [8]byte
	binary.LittleEndian.PutUint64(buf[:], v)
	w.buf = append(w.buf, buf[:]...)
}

// WriteDouble writes a double field.
func (w *ProtoWriter) WriteDouble(fieldNumber int, v float64) {
	if v == 0 {
		return
	}
	w.WriteFixed64(fieldNumber, math.Float64bits(v))
}
