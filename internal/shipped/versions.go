package shipped

// versions is every version of each shipped file Endless has ever shipped, as
// the SHA-256 of its rendered bytes, oldest first, each commented with the
// commit that shipped it.
//
// Append the new hash in the same commit that changes a template: a test fails
// until the current rendering is listed. Never remove one — a project may still
// hold that version, and an unlisted hash reads as a local edit, so that
// project would never be updated again.
//
// The versions before E-2421 were recovered from git history, where the text
// lived in src/endless/suite_rules.py.
var versions = map[Kind][]string{
	TasksRules: {
		"3d39b67b71b51093630367d165efccd4743b71c0d386e8d5a87e39c1f906c6e6", // 53c53f8a8 E-2023
		"422ab2c34882e556ed60e4e019f157db077b7599a13b9e640455fdb13a5a0d0f", // d3cf23423 E-2023
		"6991dba0b3d5e9693263babae05bb82c1f97ebee15c3f92200d6aa5faaaae118", // 53376963c E-2090
		"9498b7168700ff05585fa870ad2fbe63bb101e4f7d46a7267d8b2e3c512485a3", // 0f0471711 E-2137
		"88a054e07e97c74fa549270f2166169d3b206dc262a2d9e676d3490d95d5fb50", // 31c0b1560 E-1531
		"533d55ae48924c718ddf4c6866959f45bf8cf901f6096700f1895eacdf0b342b", // a375958ab E-1993
		"c71b241aba9ff7b2366324fa870c5fe66550ba48a1e4cdb79cf5416bd0451e13", // 1b6f91f5d E-2192
		"939e9efb9ea3ac91aaf4f43f3f450c4afd75fa6866672d4e556fd3cac71f166c", // acba487e7 E-2192
		"35ebf808a996a819a36d84c77f95c7fadef3d700180a2241d1b0bfa61faf8a7d", // afcb91d07 E-2243
		"6e317352960079f3240c1eaf78183cd97935e37cf42fa9bb108382c8c09832de", // a8ab0e478 E-2225
		"c87d23a8f1a01b522d027b26bd0843510f5bf83b19df95c424d28d17f88f40f2", // 7bce7a2d3 E-2278
		"3d680c983022d2ce0a876dc5db858265c6282289c1a7337a14654453e8c31cbc", // 135121838 E-2278
		"c4ef62e7ef0af38c28913dabd8249a6c54b983a1ccc14c996739e63997b223f9", // 2e1e17514 E-1562
		"ad1f7982794863bb8b896f0ac749b47e684c720765245464b77d3dcae325a587", // 1c419dd6f E-2240
		"97ea5097d3bb292dc3bec0828da13e6dc89332b4bd367398d1b27a6e16a4a276", // 7fabd3e41 E-2419
	},
}
