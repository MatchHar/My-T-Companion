# Companion 1.10.60

[English](RELEASE_NOTES_1.10.60.md) · [简体中文](RELEASE_NOTES_1.10.60.zh-Hans.md) · [繁體中文](RELEASE_NOTES_1.10.60.zh-Hant.md)

New read-only owner endpoint `GET /api/companion/v1/cars/<id>/road-coverage`
(capability `road_coverage_v1`) for the My T footprint map. It returns
decimated drive trails (about 60 m spacing, at most 80,000 points) and 200 m
visit cells for an optional `from`/`to` range. Owner token only; nothing is
written, shared, or sent to the relay. A `mode` field is reserved for future
driving-mode colouring.
