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

type metaDoc struct {
	Op     string        `bson:"op"`
	ID     bson.RawValue `bson:"_id,omitempty"`
	Before bson.Raw      `bson:"before,omitempty"`
}

func makeMetaDocument(fields bsonFields) (metaDoc, error) {
	var res metaDoc
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
	case "delete":
		res.Op = "d"
		res.ID = fields.DocumentKey.ID
	default:
		return metaDoc{}, fmt.Errorf("unknown operation: %q", fields.OperationType)
	}

	return res, nil
}

func main() {
	var count int64

	dec := bson.NewDecoder(bson.NewDocumentReader(os.Stdin))

	buf := new(bytes.Buffer)
	enc := bson.NewEncoder(&SanitizedValueWriter{
		ValueWriter: bson.NewExtJSONValueWriter(buf, false, false),
	})

	for {
		var fields bsonFields
		err := dec.Decode(&fields)
		if err == io.EOF {
			break
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error unmarshaling BSON document: %v\n", err)
			os.Exit(1)
		}

		meta, err := makeMetaDocument(fields)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error creating event document: %v\n", err)
			os.Exit(1)
		}

		// If the operation is a delete, we're just going to add the "_meta"
		// field. Otherwise, we need to add the fields from "FullDocument".
		if fields.OperationType == "delete" {
			buf.WriteByte('{')
		} else {
			err := enc.Encode(fields.FullDocument)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error marshaling fullDocument to Extended JSON: %v\n", err)
				os.Exit(1)
			}
			idx := bytes.LastIndexByte(buf.Bytes(), '}')
			if idx == -1 {
				fmt.Fprintf(os.Stderr, "Could not find closing bracket in Extended JSON document\n")
				os.Exit(1)
			}
			buf.Truncate(idx)  // Remove trailing "}"
			buf.WriteByte(',') // Add comma to prepare for adding the next field
		}

		buf.WriteString(`"_meta":`)

		err = enc.Encode(meta)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error marshaling to Extended JSON: %v\n", err)
			os.Exit(1)
		}

		// Remove trailing newline added by Encode then add the closing "}"
		// bracket and newline.
		idx := bytes.LastIndexByte(buf.Bytes(), '\n')
		if idx > 0 {
			buf.Truncate(idx)
		}
		buf.WriteString("}\n")

		ej := buf.Bytes()
		buf.Reset()

		count += int64(len(ej))
	}

	fmt.Println(count)
}

type SanitizedValueWriter struct {
	bson.ValueWriter
}

func (svw *SanitizedValueWriter) WriteDouble(v float64) error {
	if math.IsNaN(v) {
		return svw.WriteString("NaN")
	}
	if math.IsInf(v, +1) {
		return svw.WriteString("Infinity")
	}
	if math.IsInf(v, -1) {
		return svw.WriteString("-Infinity")
	}
	return svw.ValueWriter.WriteDouble(v)
}

func (svw *SanitizedValueWriter) WriteDateTime(dt int64) error {
	if dt < minTimeMilli {
		dt = minTimeMilli
	} else if dt > maxTimeMilli {
		dt = maxTimeMilli
	}
	return svw.ValueWriter.WriteDateTime(dt)
}

func (svw *SanitizedValueWriter) WriteObjectID(oid bson.ObjectID) error {
	return svw.ValueWriter.WriteString(oid.Hex())
}

func (svw *SanitizedValueWriter) WriteDocument() (bson.DocumentWriter, error) {
	dw, err := svw.ValueWriter.WriteDocument()
	if err != nil {
		return nil, err
	}
	return &SanitizedDocumentWriter{DocumentWriter: dw}, nil
}

func (svw *SanitizedValueWriter) WriteArray() (bson.ArrayWriter, error) {
	aw, err := svw.ValueWriter.WriteArray()
	if err != nil {
		return nil, err
	}
	return &SanitizedArrayWriter{ArrayWriter: aw}, nil
}

type SanitizedDocumentWriter struct {
	bson.DocumentWriter
}

func (sdw *SanitizedDocumentWriter) WriteDocumentElement(key string) (bson.ValueWriter, error) {
	vw, err := sdw.DocumentWriter.WriteDocumentElement(key)
	if err != nil {
		return nil, err
	}
	return &SanitizedValueWriter{ValueWriter: vw}, nil
}

type SanitizedArrayWriter struct {
	bson.ArrayWriter
}

func (saw *SanitizedArrayWriter) WriteArrayElement() (bson.ValueWriter, error) {
	vw, err := saw.ArrayWriter.WriteArrayElement()
	if err != nil {
		return nil, err
	}
	return &SanitizedValueWriter{ValueWriter: vw}, nil
}
