# Changelog

## [0.4.1](https://github.com/auron-labs/dnsync/compare/v0.4.1...v0.4.1) (2026-11-19)


### Features

* **01-01:** implement config-driven startup and cancellable runtime ([50b3b10](https://github.com/auron-labs/dnsync/commit/50b3b10eabfd9e15a47de39da2ddb605a79168b7))
* **01-02:** add config validation and secret resolution ([00bc242](https://github.com/auron-labs/dnsync/commit/00bc24280d4ab9b745717b3ff543d7b4d2d4c708))
* **01-03:** add provider contracts and runtime factories ([d7b4197](https://github.com/auron-labs/dnsync/commit/d7b4197a45717d26e02ad301271d99459b806c86))
* **01-04:** wire atomic state and runtime deps ([6650d7b](https://github.com/auron-labs/dnsync/commit/6650d7b325b4362d3c88b710da06dd976db41f74))
* **02-01:** implement ownership-safe reconcile planner and apply flow ([9692cc6](https://github.com/auron-labs/dnsync/commit/9692cc6e03205fbb897e26ad37781660a03e5856))
* **02-02:** implement real AdGuard output transport ([e7c53d2](https://github.com/auron-labs/dnsync/commit/e7c53d220d137703b1576279e245a6842e2abb00))
* **03-04:** complete docker sync automation and runtime recovery ([a2baa87](https://github.com/auron-labs/dnsync/commit/a2baa872de954958ab4fe24cab3d7ac733908691))
* add managed Cloudflare tunnel lifecycle ([ef7cdf8](https://github.com/auron-labs/dnsync/commit/ef7cdf8cf1baf12913ddd5b313995c6cb5cc5990))
* **config:** add source host ip support ([e127734](https://github.com/auron-labs/dnsync/commit/e1277340fe1d22262eb5a0f46f2cc8d6fd61e797))
* **docker:** support Compose project exclusions ([0ef3766](https://github.com/auron-labs/dnsync/commit/0ef3766a70b994e4f724acc654ffbab45ede251c))
* fix logging ([7ad3c18](https://github.com/auron-labs/dnsync/commit/7ad3c1841d8fbaa975d8bd7c5341a03ecdcce211))
* improve state reconciliation ([6a5dcbb](https://github.com/auron-labs/dnsync/commit/6a5dcbb4a951eb03e1c83a1226da594f0ed3cdfb))
* **pihole:** add Pi-hole v6 local DNS output provider ([8bbff62](https://github.com/auron-labs/dnsync/commit/8bbff623b3785c0fd831575a634afcfbfd4b0e72))
* report errors to Sentry ([46f164e](https://github.com/auron-labs/dnsync/commit/46f164e9cf22b44f31568dc344be08145e04f54f))
* **technitium:** add Technitium DNS zone output provider ([7f92c76](https://github.com/auron-labs/dnsync/commit/7f92c76391e6cf83334afd4753e71646631310b1))
* **technitium:** report record provenance for listed records ([f41c7c9](https://github.com/auron-labs/dnsync/commit/f41c7c93fe779bb7d23f4d737d443246fee524ea))
* **telltaledns:** add local names output provider ([9b8b2a7](https://github.com/auron-labs/dnsync/commit/9b8b2a72f76caea8ea79ca7fcafbba5737c36082))


### Bug Fixes

* **02-01:** run startup reconciliation in runtime ([d496656](https://github.com/auron-labs/dnsync/commit/d49665643ce57f65b76efb159e13e6470ab47a7c))
* **05:** close milestone audit gaps ([6c88238](https://github.com/auron-labs/dnsync/commit/6c882380f502a2e70e80ed4aadd1ecf065a1820f))
* **cloudflare:** recover duplicate create conflicts ([b3254a7](https://github.com/auron-labs/dnsync/commit/b3254a7b1b1fd3ac1eb9f93a791b45e2d918a394))
* **cloudflare:** take over existing single-host records ([840d126](https://github.com/auron-labs/dnsync/commit/840d126049f9fa23e735de6df596193717f15fac))
* **config:** add docker base domain support ([fa8920a](https://github.com/auron-labs/dnsync/commit/fa8920a1ef0cf88ec0677f379994a9e712e1b653))
* **docker:** add proxy.dns output targeting ([1008c77](https://github.com/auron-labs/dnsync/commit/1008c7721bef5602801e0ce08081f97a7572b454))
* **pihole:** restart FTL once when replacing a CNAME ([7bc048e](https://github.com/auron-labs/dnsync/commit/7bc048e3e74012a5dc23306cbdfb5f3bf81c8077))
* **release:** migrate goreleaser config ([e65efd1](https://github.com/auron-labs/dnsync/commit/e65efd110d68171fa8f72a23ee66af2cf0c834a4))
* resolve golangci-lint findings ([743976c](https://github.com/auron-labs/dnsync/commit/743976c27f5538c68012f05b892f3b184891fcc0))
* resolve post-merge conflicts from wave 1 ([355d3df](https://github.com/auron-labs/dnsync/commit/355d3df2208c0026d0aa336e9efef3f37a9c9c22))
* **runtime:** add trace logging and fix cloudflare visible matching ([772748d](https://github.com/auron-labs/dnsync/commit/772748d6fb0a332a83b5daedad1db81cee5770c5))
* **runtime:** finish tunnel teardown when unrelated outputs fail ([579ec58](https://github.com/auron-labs/dnsync/commit/579ec58b54eb79e31ab3a5531c8c0fdc87706f42))
* **runtime:** keep running when a DNS server or record fails ([39ce197](https://github.com/auron-labs/dnsync/commit/39ce19731620f71df812b9a40ffc57d616b74602))
* **runtime:** recover owned same-host drift ([0662621](https://github.com/auron-labs/dnsync/commit/0662621a034e5aaffbaabc1177a075410bc39e0b))
* **runtime:** retry transient source reads ([a8b8fa2](https://github.com/auron-labs/dnsync/commit/a8b8fa2e3324d1b8166ed39ef49a4da7f42b61de))


### Miscellaneous Chores

* release v0.4.1 with published binaries ([6bb0448](https://github.com/auron-labs/dnsync/commit/6bb0448133197ec00c1b87a5aa17e614a759a3e7))

## [0.4.1](https://github.com/auron-labs/dnsync/compare/v0.4.0...v0.4.1) (2026-11-19)


### Miscellaneous Chores

* release v0.4.1 with published binaries ([6bb0448](https://github.com/auron-labs/dnsync/commit/6bb0448133197ec00c1b87a5aa17e614a759a3e7))

## [0.4.0](https://github.com/auron-labs/dnsync/compare/v0.2.1...v0.4.0) (2026-11-16)


### Features

* **01-01:** implement config-driven startup and cancellable runtime ([50b3b10](https://github.com/auron-labs/dnsync/commit/50b3b10eabfd9e15a47de39da2ddb605a79168b7))
* **01-02:** add config validation and secret resolution ([00bc242](https://github.com/auron-labs/dnsync/commit/00bc24280d4ab9b745717b3ff543d7b4d2d4c708))
* **01-03:** add provider contracts and runtime factories ([d7b4197](https://github.com/auron-labs/dnsync/commit/d7b4197a45717d26e02ad301271d99459b806c86))
* **01-04:** wire atomic state and runtime deps ([6650d7b](https://github.com/auron-labs/dnsync/commit/6650d7b325b4362d3c88b710da06dd976db41f74))
* **02-01:** implement ownership-safe reconcile planner and apply flow ([9692cc6](https://github.com/auron-labs/dnsync/commit/9692cc6e03205fbb897e26ad37781660a03e5856))
* **02-02:** implement real AdGuard output transport ([e7c53d2](https://github.com/auron-labs/dnsync/commit/e7c53d220d137703b1576279e245a6842e2abb00))
* **03-04:** complete docker sync automation and runtime recovery ([a2baa87](https://github.com/auron-labs/dnsync/commit/a2baa872de954958ab4fe24cab3d7ac733908691))
* add managed Cloudflare tunnel lifecycle ([ef7cdf8](https://github.com/auron-labs/dnsync/commit/ef7cdf8cf1baf12913ddd5b313995c6cb5cc5990))
* **config:** add source host ip support ([e127734](https://github.com/auron-labs/dnsync/commit/e1277340fe1d22262eb5a0f46f2cc8d6fd61e797))
* **docker:** support Compose project exclusions ([0ef3766](https://github.com/auron-labs/dnsync/commit/0ef3766a70b994e4f724acc654ffbab45ede251c))
* fix logging ([7ad3c18](https://github.com/auron-labs/dnsync/commit/7ad3c1841d8fbaa975d8bd7c5341a03ecdcce211))
* improve state reconciliation ([6a5dcbb](https://github.com/auron-labs/dnsync/commit/6a5dcbb4a951eb03e1c83a1226da594f0ed3cdfb))
* **pihole:** add Pi-hole v6 local DNS output provider ([8bbff62](https://github.com/auron-labs/dnsync/commit/8bbff623b3785c0fd831575a634afcfbfd4b0e72))
* report errors to Sentry ([46f164e](https://github.com/auron-labs/dnsync/commit/46f164e9cf22b44f31568dc344be08145e04f54f))
* **technitium:** add Technitium DNS zone output provider ([7f92c76](https://github.com/auron-labs/dnsync/commit/7f92c76391e6cf83334afd4753e71646631310b1))
* **technitium:** report record provenance for listed records ([f41c7c9](https://github.com/auron-labs/dnsync/commit/f41c7c93fe779bb7d23f4d737d443246fee524ea))
* **telltaledns:** add local names output provider ([9b8b2a7](https://github.com/auron-labs/dnsync/commit/9b8b2a72f76caea8ea79ca7fcafbba5737c36082))


### Bug Fixes

* **02-01:** run startup reconciliation in runtime ([d496656](https://github.com/auron-labs/dnsync/commit/d49665643ce57f65b76efb159e13e6470ab47a7c))
* **05:** close milestone audit gaps ([6c88238](https://github.com/auron-labs/dnsync/commit/6c882380f502a2e70e80ed4aadd1ecf065a1820f))
* **cloudflare:** recover duplicate create conflicts ([b3254a7](https://github.com/auron-labs/dnsync/commit/b3254a7b1b1fd3ac1eb9f93a791b45e2d918a394))
* **cloudflare:** take over existing single-host records ([840d126](https://github.com/auron-labs/dnsync/commit/840d126049f9fa23e735de6df596193717f15fac))
* **config:** add docker base domain support ([fa8920a](https://github.com/auron-labs/dnsync/commit/fa8920a1ef0cf88ec0677f379994a9e712e1b653))
* **docker:** add proxy.dns output targeting ([1008c77](https://github.com/auron-labs/dnsync/commit/1008c7721bef5602801e0ce08081f97a7572b454))
* **pihole:** restart FTL once when replacing a CNAME ([7bc048e](https://github.com/auron-labs/dnsync/commit/7bc048e3e74012a5dc23306cbdfb5f3bf81c8077))
* **release:** migrate goreleaser config ([e65efd1](https://github.com/auron-labs/dnsync/commit/e65efd110d68171fa8f72a23ee66af2cf0c834a4))
* resolve golangci-lint findings ([743976c](https://github.com/auron-labs/dnsync/commit/743976c27f5538c68012f05b892f3b184891fcc0))
* resolve post-merge conflicts from wave 1 ([355d3df](https://github.com/auron-labs/dnsync/commit/355d3df2208c0026d0aa336e9efef3f37a9c9c22))
* **runtime:** add trace logging and fix cloudflare visible matching ([772748d](https://github.com/auron-labs/dnsync/commit/772748d6fb0a332a83b5daedad1db81cee5770c5))
* **runtime:** finish tunnel teardown when unrelated outputs fail ([579ec58](https://github.com/auron-labs/dnsync/commit/579ec58b54eb79e31ab3a5531c8c0fdc87706f42))
* **runtime:** keep running when a DNS server or record fails ([39ce197](https://github.com/auron-labs/dnsync/commit/39ce19731620f71df812b9a40ffc57d616b74602))
* **runtime:** recover owned same-host drift ([0662621](https://github.com/auron-labs/dnsync/commit/0662621a034e5aaffbaabc1177a075410bc39e0b))
* **runtime:** retry transient source reads ([a8b8fa2](https://github.com/auron-labs/dnsync/commit/a8b8fa2e3324d1b8166ed39ef49a4da7f42b61de))

## [0.2.1](https://github.com/aaronflorey/docker-dns-sync/compare/v0.2.0...v0.2.1) (2026-06-21)


### Bug Fixes

* **docker:** add proxy.dns output targeting ([1ac8811](https://github.com/aaronflorey/docker-dns-sync/commit/1ac88110ea063467bd7bde8ad5c1cb45faf194ba))

## [0.2.0](https://github.com/aaronflorey/docker-dns-sync/compare/v0.1.0...v0.2.0) (2026-05-28)


### Features

* **01-01:** implement config-driven startup and cancellable runtime ([cf45aca](https://github.com/aaronflorey/docker-dns-sync/commit/cf45acabc2451709fb7ae09a956e3724c0ffb287))
* **01-02:** add config validation and secret resolution ([45b0e6a](https://github.com/aaronflorey/docker-dns-sync/commit/45b0e6ac04e74351d80a6a10be5adbb2756e2c3b))
* **01-03:** add provider contracts and runtime factories ([0fd7d6f](https://github.com/aaronflorey/docker-dns-sync/commit/0fd7d6f1dc2419f64f545ba513b172d8bc674271))
* **01-04:** wire atomic state and runtime deps ([4925df8](https://github.com/aaronflorey/docker-dns-sync/commit/4925df847573a3f04bb9534173c3edd44b6738e0))
* **02-01:** implement ownership-safe reconcile planner and apply flow ([316c220](https://github.com/aaronflorey/docker-dns-sync/commit/316c220ed345874bf7067b6ed90d467a7cf78ec3))
* **02-02:** implement real AdGuard output transport ([f7151c5](https://github.com/aaronflorey/docker-dns-sync/commit/f7151c538ce486cba6f3ae3f834f107cfb647dbb))
* **03-04:** complete docker sync automation and runtime recovery ([9924c4f](https://github.com/aaronflorey/docker-dns-sync/commit/9924c4f49f527aa7ec237bd051ab9c5710dfd473))
* **config:** add source host ip support ([5f8f944](https://github.com/aaronflorey/docker-dns-sync/commit/5f8f944027f8658560a8053c7dc8d4b8ce90bf8d))
* fix logging ([8077791](https://github.com/aaronflorey/docker-dns-sync/commit/80777911b171ae56e855fc01b6f5563cede75625))


### Bug Fixes

* **02-01:** run startup reconciliation in runtime ([bbaf584](https://github.com/aaronflorey/docker-dns-sync/commit/bbaf5849e68d8888e0e14b19eb32c2510d0294ec))
* **05:** close milestone audit gaps ([2858dd7](https://github.com/aaronflorey/docker-dns-sync/commit/2858dd78debd56f12a015cdd491de54420fdd9e4))
* **cloudflare:** recover duplicate create conflicts ([4046e4d](https://github.com/aaronflorey/docker-dns-sync/commit/4046e4d0b9ff31e7f74bae26b95a4aea59151318))
* **cloudflare:** take over existing single-host records ([8d5af94](https://github.com/aaronflorey/docker-dns-sync/commit/8d5af946b088229e7a83b82c4a50cdbf046dd05c))
* **config:** add docker base domain support ([b9b1d56](https://github.com/aaronflorey/docker-dns-sync/commit/b9b1d567954e38fe696d87797bd015083345d324))
* **release:** migrate goreleaser config ([4c47f2b](https://github.com/aaronflorey/docker-dns-sync/commit/4c47f2b75ec00fc0221435dd4f9faf28948271fc))
* resolve post-merge conflicts from wave 1 ([712477b](https://github.com/aaronflorey/docker-dns-sync/commit/712477b85c9b68cbf9ba28334d8d793d4c739c47))
* **runtime:** add trace logging and fix cloudflare visible matching ([22df5da](https://github.com/aaronflorey/docker-dns-sync/commit/22df5daa1afc88275798d46043dd62d3d06bad91))
* **runtime:** recover owned same-host drift ([5242731](https://github.com/aaronflorey/docker-dns-sync/commit/52427318242eddd5babd9c0e9b04dfaba13c5a6f))
* **runtime:** retry transient source reads ([ab47fbd](https://github.com/aaronflorey/docker-dns-sync/commit/ab47fbdbf170ebe3395daf6e2672573463b58ebb))
