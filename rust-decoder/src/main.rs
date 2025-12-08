use bson::{oid::ObjectId, raw::RawBsonRef, RawDocumentBuf, RawDeserializer};
use serde::Deserialize;
use serde_json::{Map, Value};
use std::hint::black_box;
use std::io::{self, BufReader, Read};

// MongoDB considers datetimes outside of the 0-9999 year range to be _unsafe_
// so we enforce this constraint in our connector.
// see https://www.mongodb.com/docs/manual/reference/method/Date/#behavior
const MAX_TIME_MILLI: i64 = 253402300799999;
const MIN_TIME_MILLI: i64 = -62167219200000;

#[derive(Deserialize)]
struct DocKey {
    #[serde(rename = "_id")]
    id: ObjectId,
}

#[derive(Deserialize)]
struct Namespace {
    db: String,
    coll: String,
}

// Use RawDocumentBuf for the nested documents so we can transcode directly from bytes
#[derive(Deserialize)]
#[serde(rename_all = "camelCase")]
struct BsonFields<'a> {
    document_key: DocKey,
    operation_type: String,
    #[serde(borrow)]
    full_document: RawBsonRef<'a>,
    #[serde(borrow)]
    full_document_before_change: RawBsonRef<'a>,
    #[allow(dead_code)]
    ns: Namespace,
}

fn id_to_string(value: &Value) -> String {
    match value {
        Value::String(s) => s.clone(),
        _ => serde_json::to_string(value).unwrap_or_default(),
    }
}

fn sanitize_primitive(value: Value) -> Value {
    match value {
        Value::Number(n) => {
            if let Some(f) = n.as_f64() {
                if f.is_nan() {
                    return Value::String("NaN".to_string());
                } else if f.is_infinite() {
                    if f.is_sign_positive() {
                        return Value::String("Infinity".to_string());
                    } else {
                        return Value::String("-Infinity".to_string());
                    }
                }
            }
            Value::Number(n)
        }
        Value::Object(map) => {
            // Check if this is an extended JSON datetime: {"$date": {"$numberLong": "..."}}
            if let Some(date_val) = map.get("$date") {
                if let Some(obj) = date_val.as_object() {
                    if let Some(Value::String(num_str)) = obj.get("$numberLong") {
                        if let Ok(millis) = num_str.parse::<i64>() {
                            let clamped = millis.clamp(MIN_TIME_MILLI, MAX_TIME_MILLI);
                            if clamped != millis {
                                let mut new_map = Map::new();
                                let mut inner = Map::new();
                                inner.insert("$numberLong".to_string(), Value::String(clamped.to_string()));
                                new_map.insert("$date".to_string(), Value::Object(inner));
                                return Value::Object(new_map);
                            }
                        }
                    }
                }
                return Value::Object(map);
            }
            Value::Object(sanitize_document(map, false))
        }
        Value::Array(arr) => Value::Array(sanitize_array(arr)),
        other => other,
    }
}

fn sanitize_document(mut doc: Map<String, Value>, is_root: bool) -> Map<String, Value> {
    for (key, value) in doc.iter_mut() {
        let owned_value = std::mem::take(value);
        if is_root && key == "_id" {
            *value = Value::String(id_to_string(&owned_value));
        } else {
            *value = sanitize_primitive(owned_value);
        }
    }
    doc
}

fn sanitize_array(arr: Vec<Value>) -> Vec<Value> {
    arr.into_iter().map(sanitize_primitive).collect()
}

fn transcode_and_sanitize(raw_doc: &bson::RawDocument, is_root: bool) -> Result<Map<String, Value>, Box<dyn std::error::Error>> {
    let de = RawDeserializer::new(raw_doc.as_bytes())?;
    let value: Value = serde_transcode::transcode(de, serde_json::value::Serializer)?;
    match value {
        Value::Object(m) => Ok(sanitize_document(m, is_root)),
        _ => Err("expected object".into()),
    }
}

fn make_event_document(fields: BsonFields) -> Result<Vec<u8>, Box<dyn std::error::Error>> {
    let mut meta = Map::new();

    // Check if full_document_before_change is not null
    if !matches!(fields.full_document_before_change, RawBsonRef::Null) {
        if let RawBsonRef::Document(raw_doc) = fields.full_document_before_change {
            let before_doc = transcode_and_sanitize(raw_doc, true)?;
            meta.insert("before".to_string(), Value::Object(before_doc));
        }
    }

    let op = match fields.operation_type.as_str() {
        "insert" => "c",
        "update" | "replace" => "u",
        "delete" => "d",
        op => return Err(format!("unknown operation: {}", op).into()),
    };
    meta.insert("op".to_string(), Value::String(op.to_string()));

    let mut doc: Map<String, Value> = if fields.operation_type == "delete" {
        let mut d = Map::new();
        d.insert("_id".to_string(), Value::String(fields.document_key.id.to_hex()));
        d
    } else {
        if let RawBsonRef::Document(raw_doc) = fields.full_document {
            transcode_and_sanitize(raw_doc, true)?
        } else {
            return Err("fullDocument is not a document".into());
        }
    };

    doc.insert("_meta".to_string(), Value::Object(meta));

    Ok(serde_json::to_vec(&doc)?)
}

fn main() -> io::Result<()> {
    let stdin = io::stdin();
    let mut reader = BufReader::new(stdin.lock());
    let mut count: u64 = 0;

    // Buffer for reading document length
    let mut len_buf = [0u8; 4];

    loop {
        // Read document length (first 4 bytes)
        match reader.read_exact(&mut len_buf) {
            Ok(()) => {}
            Err(e) if e.kind() == io::ErrorKind::UnexpectedEof => break,
            Err(e) => return Err(e),
        }

        let doc_len = i32::from_le_bytes(len_buf) as usize;

        // Read full document
        let mut doc_bytes = vec![0u8; doc_len];
        doc_bytes[..4].copy_from_slice(&len_buf);
        reader.read_exact(&mut doc_bytes[4..])?;

        // Parse as RawDocumentBuf
        let raw_doc = RawDocumentBuf::from_bytes(doc_bytes)
            .map_err(|e| io::Error::new(io::ErrorKind::InvalidData, e))?;

        // Deserialize using RawDeserializer - this borrows from raw_doc
        let de = RawDeserializer::new(raw_doc.as_bytes())
            .map_err(|e| io::Error::new(io::ErrorKind::InvalidData, e))?;

        let fields: BsonFields = serde::Deserialize::deserialize(de)
            .map_err(|e| io::Error::new(io::ErrorKind::InvalidData, e))?;

        match make_event_document(fields) {
            Ok(json_bytes) => {
                count += black_box(json_bytes.len() as u64);
            }
            Err(e) => {
                eprintln!("Error making event document: {}", e);
                std::process::exit(1);
            }
        }
    }

    println!("{}", count);
    Ok(())
}
