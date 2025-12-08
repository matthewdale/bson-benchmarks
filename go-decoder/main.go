package main

import (
	"bufio"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
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
	Id interface{} `bson:"_id"`
}

type namespace struct {
	Database   string `bson:"db"`
	Collection string `bson:"coll"`
}

type bsonFields struct {
	DocumentKey              docKey    `bson:"documentKey"`
	OperationType            string    `bson:"operationType"`
	FullDocument             bson.M    `bson:"fullDocument"`
	FullDocumentBeforeChange bson.M    `bson:"fullDocumentBeforeChange"`
	Ns                       namespace `bson:"ns"`
}

func sanitizePrimitive(input interface{}) interface{} {
	switch v := input.(type) {
	case primitive.DateTime:
		if v < minTimeMilli {
			return primitive.DateTime(minTimeMilli)
		} else if v > maxTimeMilli {
			return primitive.DateTime(maxTimeMilli)
		}
	case float64:
		if math.IsNaN(v) {
			return "NaN"
		} else if math.IsInf(v, +1) {
			return "Infinity"
		} else if math.IsInf(v, -1) {
			return "-Infinity"
		}
	case map[string]interface{}:
		return sanitizeDocument(v)
	case primitive.M:
		return sanitizeDocument(v)
	case []interface{}:
		return sanitizeArray(v)
	case primitive.A:
		return sanitizeArray(v)
	}

	return input
}

func sanitizeDocument(doc map[string]interface{}) map[string]interface{} {
	for key, value := range doc {
		if key == idProperty {
			doc[key] = idToString(value)
		} else {
			doc[key] = sanitizePrimitive(value)
		}
	}

	return doc
}

func sanitizeArray(arr []interface{}) []interface{} {
	for i, value := range arr {
		arr[i] = sanitizePrimitive(value)
	}

	return arr
}

func idToString(value interface{}) string {
	switch v := value.(type) {
	case string:
		return v
	case primitive.ObjectID:
		return v.Hex()
	}

	var j, err = json.Marshal(value)
	if err != nil {
		panic(fmt.Sprintf("could not marshal interface{} to json: %s", err))
	}
	return string(j)
}

func makeEventDocument(fields bsonFields) (json.RawMessage, error) {
	var doc map[string]any
	meta := map[string]any{}
	if fields.FullDocumentBeforeChange != nil {
		meta[beforeProperty] = sanitizeDocument(fields.FullDocumentBeforeChange)
	}

	switch fields.OperationType {
	case "insert", "update", "replace":
		if fields.OperationType == "update" || fields.OperationType == "replace" {
			meta[opProperty] = "u"
		} else if fields.OperationType == "insert" {
			meta[opProperty] = "c"
		}
		doc = sanitizeDocument(fields.FullDocument)
	case "delete":
		meta[opProperty] = "d"
		doc = map[string]any{idProperty: idToString(fields.DocumentKey.Id)}
	default:
		return nil, fmt.Errorf("unknown operation: %q", fields.OperationType)
	}

	doc[metaProperty] = meta

	docBytes, err := json.Marshal(doc)
	if err != nil {
		return nil, fmt.Errorf("serializing document: %w", err)
	}

	return docBytes, nil
}

func main() {
	reader := bufio.NewReader(os.Stdin)
	var count int64

	for {
		// Read 4-byte length prefix (little-endian)
		lengthBytes := make([]byte, 4)
		_, err := io.ReadFull(reader, lengthBytes)
		if err == io.EOF {
			break
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error reading length: %v\n", err)
			os.Exit(1)
		}

		length := binary.LittleEndian.Uint32(lengthBytes)

		// BSON document length includes the 4 length bytes themselves
		docBytes := make([]byte, length)
		copy(docBytes, lengthBytes)

		// Read the rest of the document
		_, err = io.ReadFull(reader, docBytes[4:])
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error reading document: %v\n", err)
			os.Exit(1)
		}

		// Decode into struct
		var fields bsonFields
		err = bson.Unmarshal(docBytes, &fields)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error unmarshaling document: %v\n", err)
			os.Exit(1)
		}

		// Process the event document
		jsonBytes, err := makeEventDocument(fields)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error making event document: %v\n", err)
			os.Exit(1)
		}

		count += int64(len(jsonBytes))
	}

	fmt.Println(count)
}
