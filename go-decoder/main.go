package main

import (
	"bytes"
	"fmt"
	"io"
	"math"
	"os"

	"go.mongodb.org/mongo-driver/v2/bson"
)

const (
	// MongoDB considers datetimes outside of the 0-9999 year range to be _unsafe_
	// so we enforce this constraint in our connector.
	// see https://www.mongodb.com/docs/manual/reference/method/Date/#behavior
	maxTimeMilli = 253402300799999
	minTimeMilli = -62167219200000
)

type docKey struct {
	ID bson.RawValue `bson:"_id"`
}

type namespace struct {
	Database   string `bson:"db"`
	Collection string `bson:"coll"`
}

type bsonFields struct {
	DocumentKey              docKey    `bson:"documentKey"`
	OperationType            string    `bson:"operationType"`
	FullDocument             bson.Raw  `bson:"fullDocument"`
	FullDocumentBeforeChange bson.Raw  `bson:"fullDocumentBeforeChange"`
	Ns                       namespace `bson:"ns"`
}

type result struct {
	ID       bson.RawValue `bson:"_id,omitempty"`
	Op       string        `bson:"op"`
	Document bson.Raw      `bson:"document,omitempty"`
	Before   bson.Raw      `bson:"before"`
}

func makeEventDocument(fields bsonFields) (result, error) {
	var res result
	if fields.FullDocumentBeforeChange != nil {
		res.Before = fields.FullDocumentBeforeChange
	}

	switch fields.OperationType {
	case "insert", "update", "replace":
		switch fields.OperationType {
		case "update", "replace":
			res.Op = "u"
		case "insert":
			res.Op = "c"
		}
		res.Document = fields.FullDocument
	case "delete":
		res.Op = "d"
		res.ID = fields.DocumentKey.ID
	default:
		return result{}, fmt.Errorf("unknown operation: %q", fields.OperationType)
	}

	return res, nil
}

func main() {
	var count int64

	dec := bson.NewDecoder(bson.NewDocumentReader(os.Stdin))

	buf := new(bytes.Buffer)
	enc := bson.NewEncoder(&JSONValueWriter{
		ValueWriter: bson.NewExtJSONValueWriter(buf, false, false),
	})

	for {
		var res bsonFields
		err := dec.Decode(&res)
		if err == io.EOF {
			break
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error unmarshaling BSON document: %v\n", err)
			os.Exit(1)
		}

		doc, err := makeEventDocument(res)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error creating event document: %v\n", err)
			os.Exit(1)
		}

		err = enc.Encode(doc)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error marshaling to Extended JSON: %v\n", err)
			os.Exit(1)
		}

		ej := buf.Bytes()
		buf.Reset()

		count += int64(len(ej))
	}

	fmt.Println(count)
}

type JSONValueWriter struct {
	bson.ValueWriter
}

func (jvw *JSONValueWriter) WriteDouble(v float64) error {
	if math.IsNaN(v) {
		return jvw.WriteString("NaN")
	}
	if math.IsInf(v, +1) {
		return jvw.WriteString("Infinity")
	}
	if math.IsInf(v, -1) {
		return jvw.WriteString("-Infinity")
	}
	return jvw.ValueWriter.WriteDouble(v)
}

func (jvw *JSONValueWriter) WriteDateTime(dt int64) error {
	if dt < minTimeMilli {
		dt = minTimeMilli
	} else if dt > maxTimeMilli {
		dt = maxTimeMilli
	}
	return jvw.ValueWriter.WriteDateTime(dt)
}

func (jvw *JSONValueWriter) WriteObjectID(oid bson.ObjectID) error {
	return jvw.ValueWriter.WriteString(oid.Hex())
}

func (jvw *JSONValueWriter) WriteDocument() (bson.DocumentWriter, error) {
	dw, err := jvw.ValueWriter.WriteDocument()
	if err != nil {
		return nil, err
	}
	return &JSONDocumentWriter{DocumentWriter: dw}, nil
}

func (jvw *JSONValueWriter) WriteArray() (bson.ArrayWriter, error) {
	aw, err := jvw.ValueWriter.WriteArray()
	if err != nil {
		return nil, err
	}
	return &JSONArrayWriter{ArrayWriter: aw}, nil
}

type JSONDocumentWriter struct {
	bson.DocumentWriter
}

func (jdw *JSONDocumentWriter) WriteDocumentElement(key string) (bson.ValueWriter, error) {
	vw, err := jdw.DocumentWriter.WriteDocumentElement(key)
	if err != nil {
		return nil, err
	}
	return &JSONValueWriter{ValueWriter: vw}, nil
}

type JSONArrayWriter struct {
	bson.ArrayWriter
}

func (jaw *JSONArrayWriter) WriteArrayElement() (bson.ValueWriter, error) {
	vw, err := jaw.ArrayWriter.WriteArrayElement()
	if err != nil {
		return nil, err
	}
	return &JSONValueWriter{ValueWriter: vw}, nil
}

// type StringIDDocumentWriter struct {
// 	bson.DocumentWriter
// }

// func (dw *StringIDDocumentWriter) WriteDocumentElement(key string) (bson.ValueWriter, error) {
// 	vw, err := dw.DocumentWriter.WriteDocumentElement(key)
// 	if err != nil {
// 		return nil, err
// 	}
// 	if key == idProperty {
// 		return NewJSONStringWriter(vw), nil
// 	}

// 	return &JSONValueWriter{ValueWriter: vw}, nil
// }

// func NewJSONStringWriter(vw bson.ValueWriter) *JSONStringValueWriter {
// 	buf := new(bytes.Buffer)
// 	return &JSONStringValueWriter{
// 		vw:  vw,
// 		buf: buf,
// 	}
// }

// type JSONStringValueWriter struct {
// 	vw  bson.ValueWriter
// 	buf *bytes.Buffer
// }

// func (jsw *JSONStringValueWriter) WriteDocument() (bson.DocumentWriter, error) {
// 	dw, err := jsw.vw.WriteDocument()
// 	if err != nil {
// 		return nil, err
// 	}
// 	return &StringIDDocumentWriter{dw}, nil
// }

// func (jsw *JSONStringValueWriter) WriteCodeWithScope(code string) (bson.DocumentWriter, error) {
// 	panic("blah")
// }

// func (jsw *JSONStringValueWriter) WriteArray() (bson.ArrayWriter, error) {
// 	panic("blah")
// }

// func (jsw *JSONStringValueWriter) WriteObjectID(oid bson.ObjectID) error {
// 	return jsw.vw.WriteString(oid.Hex())
// }

// func (jsw *JSONStringValueWriter) WriteString(s string) error {
// 	return jsw.vw.WriteString(s)
// }

// func (jsw *JSONStringValueWriter) WriteBinary(b []byte) error {
// 	return jsw.writeAsJSON(b)
// }

// func (jsw *JSONStringValueWriter) WriteBinaryWithSubtype(b []byte, btype byte) error {
// 	return jsw.writeAsJSON(bson.Binary{Subtype: btype, Data: b})
// }

// func (jsw *JSONStringValueWriter) WriteBoolean(b bool) error {
// 	return jsw.writeAsJSON(b)
// }

// func (jsw *JSONStringValueWriter) WriteDBPointer(ns string, oid bson.ObjectID) error {
// 	return jsw.writeAsJSON(bson.DBPointer{DB: ns, Pointer: oid})
// }

// func (jsw *JSONStringValueWriter) WriteDateTime(dt int64) error {
// 	return jsw.writeAsJSON(bson.DateTime(dt))
// }

// func (jsw *JSONStringValueWriter) WriteDecimal128(d128 bson.Decimal128) error {
// 	return jsw.writeAsJSON(d128)
// }

// func (jsw *JSONStringValueWriter) WriteDouble(f float64) error {
// 	return jsw.writeAsJSON(f)
// }

// func (jsw *JSONStringValueWriter) WriteInt32(i int32) error {
// 	return jsw.writeAsJSON(i)
// }

// func (jsw *JSONStringValueWriter) WriteInt64(i int64) error {
// 	return jsw.writeAsJSON(i)
// }
// func (jsw *JSONStringValueWriter) WriteJavascript(code string) error {
// 	return jsw.writeAsJSON(code)
// }

// func (jsw *JSONStringValueWriter) WriteMaxKey() error {
// 	return jsw.writeAsJSON(bson.MaxKey{})
// }

// func (jsw *JSONStringValueWriter) WriteMinKey() error {
// 	return jsw.writeAsJSON(bson.MinKey{})
// }

// func (jsw *JSONStringValueWriter) WriteNull() error {
// 	return jsw.writeAsJSON(nil)
// }

// func (jsw *JSONStringValueWriter) WriteRegex(pattern, options string) error {
// 	return jsw.writeAsJSON(bson.Regex{Pattern: pattern, Options: options})
// }

// func (jsw *JSONStringValueWriter) WriteSymbol(symbol string) error {
// 	return jsw.writeAsJSON(symbol)
// }

// func (jsw *JSONStringValueWriter) WriteTimestamp(t, i uint32) error {
// 	return jsw.writeAsJSON(bson.Timestamp{T: t, I: i})
// }

// func (jsw *JSONStringValueWriter) WriteUndefined() error {
// 	return jsw.writeAsJSON(bson.Undefined{})
// }

// func (jsw *JSONStringValueWriter) writeAsJSON(v any) error {
// 	j, err := json.Marshal(v)
// 	if err != nil {
// 		return err
// 	}
// 	return jsw.vw.WriteString(string(j))
// }

// type JSONStringDocumentWriter struct {
// 	vw  bson.ValueWriter
// 	buf *bytes.Buffer
// }

// func NewJSONStringDocumentWriter() *JSONStringDocumentWriter {
// 	buf := new(bytes.Buffer)
// 	return &JSONStringDocumentWriter{
// 		vw:  bson.NewExtJSONValueWriter(buf, false, false),
// 		buf: buf,
// 	}
// }

// func (jdw *JSONStringDocumentWriter) WriteDocumentElement(key string) (bson.ValueWriter, error) {
// 	return jdw.vw, nil
// }

// func (jdw *JSONStringDocumentWriter) WriteDocumentEnd() error {
// 	return nil
// }
