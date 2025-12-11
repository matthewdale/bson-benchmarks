package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"

	"go.mongodb.org/mongo-driver/v2/bson"
)

const (
	idProperty     = "_id"
	metaProperty   = "_meta"
	beforeProperty = "before"
	opProperty     = "op"

	// MongoDB considers datetimes outside of the 0-9999 year range to be _unsafe_
	// so we enforce this constraint in our connector.
	// see https://www.mongodb.com/docs/manual/reference/method/Date/#behavior
	maxTimeMilli = 253402300799999
	minTimeMilli = -62167219200000
)

type docKey struct {
	Id bson.RawValue `bson:"_id"`
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

func makeEventDocument(fields bsonFields) (map[string]any, error) {
	meta := make(map[string]any, 2)
	if fields.FullDocumentBeforeChange != nil {
		fdbc, err := sanitizeDocument(fields.FullDocumentBeforeChange)
		if err != nil {
			return nil, fmt.Errorf("error sanitizing FullDocumentBeforeChange: %w", err)
		}
		meta[beforeProperty] = fdbc
	}

	var doc map[string]any
	switch fields.OperationType {
	case "insert", "update", "replace":
		if fields.OperationType == "update" || fields.OperationType == "replace" {
			meta[opProperty] = "u"
		} else if fields.OperationType == "insert" {
			meta[opProperty] = "c"
		}
		fd, err := sanitizeDocument(fields.FullDocument)
		if err != nil {
			return nil, fmt.Errorf("error sanitizing FullDocument: %w", err)
		}
		doc = fd
	case "delete":
		meta[opProperty] = "d"
		s, err := idToString(fields.DocumentKey.Id)
		if err != nil {
			return nil, fmt.Errorf("error converting _id to string: %w", err)
		}
		doc = map[string]any{idProperty: s}
	default:
		return nil, fmt.Errorf("unknown operation: %q", fields.OperationType)
	}

	doc[metaProperty] = meta

	return doc, nil
}

func sanitizeRawValue(rv bson.RawValue) (any, error) {
	switch rv.Type {
	case bson.TypeDateTime:
		dt, ok := rv.DateTimeOK()
		if !ok {
			return nil, fmt.Errorf("could not get DateTime value")
		}
		if dt < minTimeMilli {
			return bson.DateTime(minTimeMilli), nil
		}
		if dt > maxTimeMilli {
			return bson.DateTime(maxTimeMilli), nil
		}
		return bson.DateTime(dt), nil
	case bson.TypeDouble:
		f, ok := rv.DoubleOK()
		if !ok {
			return nil, fmt.Errorf("could not get Double value")
		}
		if math.IsNaN(f) {
			return "NaN", nil
		}
		if math.IsInf(f, +1) {
			return "Infinity", nil
		}
		if math.IsInf(f, -1) {
			return "-Infinity", nil
		}
		return f, nil
	case bson.TypeEmbeddedDocument:
		doc, ok := rv.DocumentOK()
		if !ok {
			return nil, fmt.Errorf("could not get EmbeddedDocument value")
		}
		return sanitizeDocument(doc)
	case bson.TypeArray:
		arr, ok := rv.ArrayOK()
		if !ok {
			return nil, fmt.Errorf("could not get Array value")
		}
		return sanitizeArray(arr)
	default:
		return rv, nil
	}
}

func sanitizeDocument(doc bson.Raw) (map[string]any, error) {
	els, err := doc.Elements()
	if err != nil {
		return nil, fmt.Errorf("error getting document elements: %w", err)
	}

	m := make(map[string]any, len(els))
	for _, el := range els {
		key := el.Key()
		val := el.Value()
		if key == idProperty {
			s, err := idToString(val)
			if err != nil {
				return nil, fmt.Errorf("error converting _id to string: %w", err)
			}
			m[key] = s
			continue
		}

		v, err := sanitizeRawValue(val)
		if err != nil {
			return nil, fmt.Errorf("error sanitizing value for key %q: %w", key, err)
		}
		m[key] = v
	}

	return m, nil
}

func sanitizeArray(arr bson.RawArray) ([]any, error) {
	values, err := arr.Values()
	if err != nil {
		return nil, fmt.Errorf("could not get array values from bson.RawArray: %w", err)
	}

	res := make([]any, len(values))
	for i := range values {
		v, err := sanitizeRawValue(values[i])
		if err != nil {
			return nil, fmt.Errorf("error sanitizing array value at index %d: %w", i, err)
		}
		res[i] = v
	}

	return res, nil
}

func idToString(rv bson.RawValue) (string, error) {
	switch rv.Type {
	case bson.TypeString:
		s, ok := rv.StringValueOK()
		if !ok {
			return "", fmt.Errorf("could not get string value")
		}
		return s, nil
	case bson.TypeObjectID:
		oid, ok := rv.ObjectIDOK()
		if !ok {
			return "", fmt.Errorf("could not get ObjectID value")
		}
		return oid.Hex(), nil
	}

	var v any
	err := rv.Unmarshal(&v)
	if err != nil {
		return "", fmt.Errorf("could not unmarshal bson.RawValue %v: %w", rv, err)
	}

	j, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("could not marshal any to json: %s", err)
	}
	return string(j), nil
}

func main() {
	var count int64

	dec := bson.NewDecoder(bson.NewDocumentReader(os.Stdin))

	buf := new(bytes.Buffer)
	enc := bson.NewEncoder(bson.NewExtJSONValueWriter(buf, false, false))

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
