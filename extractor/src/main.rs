use std::{env, error::Error, sync::Arc};

use serde_json::{json, Value};
use strawcore_core::{
    downloader::ReqwestDownloader,
    stream::{DeliveryMethod, StreamType},
    youtube::{linkhandler::search::SearchFilter, search_extractor, stream_extractor},
    NewPipe,
};

fn extract(command: &str, input: &str) -> Result<Value, Box<dyn Error>> {
    match command {
        "search" => {
            let result = search_extractor::search(input, SearchFilter::Videos)?
                .videos
                .into_iter()
                .find(|item| item.stream_type == Some(StreamType::VideoStream))
                .ok_or("No matching video found")?;
            Ok(json!({"url": result.url, "title": result.name, "artist": result.uploader_name}))
        }
        "resolve" => {
            let info = stream_extractor::stream_info(input)?;
            let stream = info
                .audio_streams
                .iter()
                .find(|stream| {
                    !stream.is_descriptive
                        && matches!(
                            stream.delivery,
                            DeliveryMethod::Progressive | DeliveryMethod::Dash
                        )
                })
                .map(|stream| &stream.url)
                .or_else(|| {
                    info.video_streams
                        .iter()
                        .find(|stream| {
                            !stream.video_only && stream.delivery == DeliveryMethod::Progressive
                        })
                        .map(|stream| &stream.url)
                })
                .ok_or("No playable stream found")?;
            Ok(json!({
                "url": stream,
                "title": info.name,
                "artist": info.uploader_name,
                "duration_seconds": info.duration_seconds
            }))
        }
        _ => Err("Expected search or resolve".into()),
    }
}

fn main() -> Result<(), Box<dyn Error>> {
    let mut args = env::args().skip(1);
    let command = args.next().ok_or("Missing command")?;
    let input = args.next().ok_or("Missing input")?;
    NewPipe::init(Arc::new(ReqwestDownloader::new()?));
    println!("{}", extract(&command, &input)?);
    Ok(())
}
