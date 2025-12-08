use bson::{doc, oid::ObjectId, Bson, Document};
use rand::Rng;
use serde::Serialize;
use std::env;
use std::io::{self, Write};

#[derive(Serialize)]
struct DocKey {
    #[serde(rename = "_id")]
    id: ObjectId,
}

#[derive(Serialize)]
struct Namespace {
    db: String,
    coll: String,
}

#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
struct BsonFields {
    document_key: DocKey,
    operation_type: String,
    full_document: Document,
    full_document_before_change: Document,
    ns: Namespace,
}

fn random_string(rng: &mut impl Rng, len: usize) -> String {
    const CHARSET: &[u8] = b"abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789";
    (0..len)
        .map(|_| CHARSET[rng.gen_range(0..CHARSET.len())] as char)
        .collect()
}

fn random_operation_type(rng: &mut impl Rng) -> String {
    const OPERATIONS: &[&str] = &["insert", "update", "replace", "delete"];
    OPERATIONS[rng.gen_range(0..OPERATIONS.len())].to_string()
}

fn generate_random_document(rng: &mut impl Rng) -> Document {
    let mut doc = Document::new();

    // Add string fields
    let name_len = rng.gen_range(5..50);
    doc.insert("name", random_string(rng, name_len));
    doc.insert("email", format!("{}@example.com", random_string(rng, 10)));
    let desc_len = rng.gen_range(20..200);
    doc.insert("description", random_string(rng, desc_len));

    // Add numeric fields
    doc.insert("age", rng.gen_range(1i32..100));
    doc.insert("score", rng.gen::<f64>() * 100.0);
    doc.insert("timestamp", rng.gen::<i64>());
    doc.insert("count", rng.gen_range(0i32..10000));

    // Add boolean
    doc.insert("active", rng.gen::<bool>());

    // Add nested document
    let street = random_string(rng, 20);
    let city = random_string(rng, 15);
    let zip: i32 = rng.gen_range(10000..99999);
    let lat = rng.gen::<f64>() * 180.0 - 90.0;
    let lng = rng.gen::<f64>() * 360.0 - 180.0;
    let nested = doc! {
        "street": street,
        "city": city,
        "zip": zip,
        "coordinates": {
            "lat": lat,
            "lng": lng
        }
    };
    doc.insert("address", nested);

    // Add array of strings
    let num_tags = rng.gen_range(2..8);
    let mut tags: Vec<Bson> = Vec::with_capacity(num_tags);
    for _ in 0..num_tags {
        let tag_len = rng.gen_range(3..15);
        tags.push(Bson::String(random_string(rng, tag_len)));
    }
    doc.insert("tags", tags);

    // Add array of numbers
    let num_scores = rng.gen_range(3..10);
    let mut scores: Vec<Bson> = Vec::with_capacity(num_scores);
    for _ in 0..num_scores {
        scores.push(Bson::Int32(rng.gen_range(0..100)));
    }
    doc.insert("scores", scores);

    doc
}

fn generate_bson_fields(rng: &mut impl Rng) -> BsonFields {
    BsonFields {
        document_key: DocKey { id: ObjectId::new() },
        operation_type: random_operation_type(rng),
        full_document: generate_random_document(rng),
        full_document_before_change: generate_random_document(rng),
        ns: Namespace {
            db: random_string(rng, 10),
            coll: random_string(rng, 15),
        },
    }
}

fn main() -> io::Result<()> {
    let args: Vec<String> = env::args().collect();
    let num_docs: usize = if args.len() > 1 {
        args[1].parse().unwrap_or(1000)
    } else {
        1000
    };

    let mut rng = rand::thread_rng();
    let mut stdout = io::stdout().lock();

    for _ in 0..num_docs {
        let doc = generate_bson_fields(&mut rng);
        let bytes = bson::to_vec(&doc).expect("Failed to serialize document");
        stdout.write_all(&bytes)?;
    }

    stdout.flush()?;
    Ok(())
}
