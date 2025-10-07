# Changelog

All notable changes to this project will be documented in this file.

## [v1.6.83]

* Fixed channel name URL encoding for channels containing special characters like "+" (e.g., "MGM+ Hits", "MGM+ Marquee")

## [v1.6.77]

* correct initial default config creation;
* work on restreaming; add playing channel stat badges; add restream connection stats;
* restreamer work; add restreamer stats; try to add channel video stats;


* fix stream buffer; rework admin;


* remove vod for now;
* add loading vid to dead streams;


* more efficient processing;
* work on parsing
* work on xc parsing
* working on stream sorter;


* fix draggable js
* drag/drop stream sorting within channels;


* fix wonky streams and fallbacks;


* add logos to channel display;
* readme update
* update license; update readme;
* update version; change playlist export url to /playlist/{group};
* restructure the admin html a bit; darken it up a bit; add /settings/custom.css to let end users customize it if they want;
* remove url and stream command in log;
* add ffmpeg


* add ffmpeg proxy;


* fix stream switching;
* manual stream stop flag;


* stream switching work;
* fix per-source filtering; fix per group filtering; work on stream watcher;
* start adding playlist filtering;
* add basic filtering to better organize xc imports; add channel paging;
* add xc parsing;
* start work on XC importer;
* fix watcher issues;
* add watcher setting;


* disable stream watcher for now;
* fix ffprobe stdin issue;
* fixed metrics issue;
* debug logging fixes in watcher;
* add better memory management for HLS streams;
* add metrics; fix rate limitter; finalize commenting;
* stream watcher fixes; working on commenting;


* err...
* walked back the watcher for now;
* readme
* version
* add stream watcher; update readme; commenting;


* fix dead stream writer; break out package;
* update readme
* change stream activator; added kill stream/revive stream;


* fixed full buffer killing stream
* fix stream selection
* trying to add stream selector
* more buffer tweaks for stable playback of funky hls channels;
* fix channel list with search; fix potential buffer issue;


* readme update
* add admin ui;
* complete rework of configuration;add headers per source; add timeout, retries, max cnx per source;"
* readme update
* fixed memory issues;
* readme work; license with proper ffmpeg/ffprobe; buffer work; corrected debug and url obfuscation;


* fix hls streams; detect beacons, ads, etc... in hls segments;
* remove proxy only; force buffer usage; add stream_timeout; add hls processing (not finished);


* updated docker-compose sample
* remove health check timout;force connection threads to connections allowed setting per stream;fix hls reconnections loop;fix min data size


* fix readme to reflect segment, rate limit removals; also quick setup updated
* remove global rate limiter
* remove global rate limiter
* rewrite readme; remove segment cache;
* add git action to compile docker image
* fix up dockercompose example
* attempt 403 fix
* add master playlist functionality
* complete modularization, along with the fixes


## [v1.0.0]

* Initial release