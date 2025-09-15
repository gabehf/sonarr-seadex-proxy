# Sonarr SeaDex Proxy

Allows you to use SeaDex (releases.moe) as a torrent indexer in Sonarr. Fetched releases are tagged with either `--SeaDex--` or `--SeaDexBest--`, depending on whether or not SeaDex has considered the release to be best.

This way, you can use custom formats that search for the `--SeaDex(Best)--` tag and add a large score, so that SeaDex torrents are considered above all others. 

The actual torrents are downloaded from Nyaa. This means that if the best release on SeaDex is in a private tracker, this proxy will not send it to sonarr. Private trackers are stupid, anyways.

## Important

Like 60% of the code in here was written by ChatGPT because I just wanted to throw something together that worked. For those interested, the LLM did okay, but I did still had to fix A LOT that it got wrong. Still decently
faster than coding from scratch, though.

I mention this to say, there is no guarantee of quality here. I made this for me, you can use it if you want. If I notice something breaks, I'll fix it, but that's about it.

## Installation

Use Docker.

```yaml
services:
  sonarr-seadex-proxy:
    image: gabehf/sonarr-seadex-proxy:latest
    container_name: sonarr-seadex-proxy
    ports:
      - 6778:6778
    restart: unless-stopped
```

## Custom Format Examples

Use these two together to find the releases marked by the proxy, then give them a big score in your quality profile so that the best releases are preferred over all others.

```json
{
  "name": "SeaDex Best",
  "includeCustomFormatWhenRenaming": false,
  "specifications": [
    {
      "name": "SeaDexBest",
      "implementation": "ReleaseTitleSpecification",
      "negate": false,
      "required": true,
      "fields": {
        "value": "--SeaDexBest--"
      }
    }
  ]
}
```
```json
{
  "name": "SeaDex Alt",
  "includeCustomFormatWhenRenaming": false,
  "specifications": [
    {
      "name": "SeaDexBest",
      "implementation": "ReleaseTitleSpecification",
      "negate": true,
      "required": false,
      "fields": {
        "value": "--SeaDexBest--"
      }
    },
    {
      "name": "SeaDex",
      "implementation": "ReleaseTitleSpecification",
      "negate": false,
      "required": true,
      "fields": {
        "value": "--SeaDex--"
      }
    }
  ]
}
```

## Notes

- This does not help Sonarr parse release titles at all. If Sonarr wasn't going to find the release anyways, it won't find it with this.
- RSS doesn't work and I don't plan on making it work.

## Albums that fueled development

Idk I just listened to random tracks off of my playlist, but you should [listen to OurR](https://www.youtube.com/watch?v=Kx0Uwa7kE78).