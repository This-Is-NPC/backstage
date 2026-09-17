# Changelog

## [0.7.0](https://github.com/This-Is-NPC/backstage/compare/v0.6.0...v0.7.0) (2026-09-17)


### Features

* **cli:** add adopt and replace-state ([c14758e](https://github.com/This-Is-NPC/backstage/commit/c14758ee9daeef2448f9d956c6b171ee39cf360d))
* **cli:** add backstage cache prune ([84a791b](https://github.com/This-Is-NPC/backstage/commit/84a791bb6c3d03b31b1c1836697bf7e034a7a434))
* **cli:** add config show ([7a8ddd8](https://github.com/This-Is-NPC/backstage/commit/7a8ddd803967f34a3766426413bf89be36953bad))
* **cli:** add play and rehearse --with-deps ([d670538](https://github.com/This-Is-NPC/backstage/commit/d6705389a154aa2ad10e601d4a4b3eea22364e7f))
* **cli:** add snapshot-delete and snapshot origins ([6e7ce67](https://github.com/This-Is-NPC/backstage/commit/6e7ce67c2fa9ff9eb8cff851c1ec8b8b4d514a4e))
* **cli:** add stage prune-states ([88e5d46](https://github.com/This-Is-NPC/backstage/commit/88e5d4630f32b9cd9f3786522af38faed97937a8))
* **cli:** add status and --json ([51266d8](https://github.com/This-Is-NPC/backstage/commit/51266d8e99169842ba14784d165bc27ea397e319))
* **cli:** add takes prune ([f6cd675](https://github.com/This-Is-NPC/backstage/commit/f6cd675a70d3855f4f3baa4a9d1e720e238938e8))
* **cli:** reserve every member stage for a group job ([d509550](https://github.com/This-Is-NPC/backstage/commit/d509550269a88bd419f2c54837d6ac490e41bc5f))
* **cli:** run planned takes as parallel child jobs ([08045f0](https://github.com/This-Is-NPC/backstage/commit/08045f0e7974d95d5a5fb796277a1345dd30a69f))
* **cli:** take image-catalog per prune-states deletion ([c59df1a](https://github.com/This-Is-NPC/backstage/commit/c59df1ae937b0f45238d71035dd1906af2831f17))
* **engine:** publish default play takes ([6f85126](https://github.com/This-Is-NPC/backstage/commit/6f851266fd9ef4919db2e125d0c61fed14f49093))
* **engine:** record capture mode on vm-end ([3a1f825](https://github.com/This-Is-NPC/backstage/commit/3a1f8256035b0e9276712543cb157e95c0524d3c))
* **engine:** record catalog wait on vm-end ([e3f408d](https://github.com/This-Is-NPC/backstage/commit/e3f408df968951ab490620cf6be59ee41f38744e))
* **engine:** record one generation per run and restore silent members ([08d90db](https://github.com/This-Is-NPC/backstage/commit/08d90dbbf3758353af97e8dd9c2465b6c19f1e7a))
* **engine:** record vm stage timings ([9dd0917](https://github.com/This-Is-NPC/backstage/commit/9dd09173c7a74f843a46ad9836a3410ad06b9f8b))
* **engine:** save vm-end after a successful take ([1469ab3](https://github.com/This-Is-NPC/backstage/commit/1469ab3f4ed1218b2f5e846cf6d3fc229d11b17f))
* **engine:** write facts for every recording ([394d2d6](https://github.com/This-Is-NPC/backstage/commit/394d2d6b135ed646b31ce0db5480f85c4f5ba611))
* **engine:** write inputs-sha256 ([7e047e3](https://github.com/This-Is-NPC/backstage/commit/7e047e370621e11e7a964e0a1ddfc46b3a03c312))
* **facts:** add capture mode and depth ([aae99fb](https://github.com/This-Is-NPC/backstage/commit/aae99fb9f5973f45feb8d37fae34199ce83e5705))
* **facts:** add catalog wait time ([8cfc87f](https://github.com/This-Is-NPC/backstage/commit/8cfc87f733bea111f25ce8828e3c043d53f95048))
* **facts:** add clip timings ([9cae4bb](https://github.com/This-Is-NPC/backstage/commit/9cae4bb25dab0e44ba5401a811849fe6e2355079))
* inherit configuration, chain states and speed up renders ([d40b7aa](https://github.com/This-Is-NPC/backstage/commit/d40b7aa5af6d4f27aac2e087907dfe6469e9aaf9))
* **machine:** capture delta images ([a6ff21f](https://github.com/This-Is-NPC/backstage/commit/a6ff21fe2cc32f67e0d94e1a709c2d393816f92a))
* **machine:** check a state-group generation before restoring ([ef825a2](https://github.com/This-Is-NPC/backstage/commit/ef825a2554cb59f46ad69f950e06ea3b35c15628))
* **machine:** record snapshot origins and replace states ([b9967d5](https://github.com/This-Is-NPC/backstage/commit/b9967d59c7b9cc08efa97b3dd1eb9bae815183d8))
* **machine:** release image-catalog during convert ([6231466](https://github.com/This-Is-NPC/backstage/commit/6231466149855760fe15450325c37de49261c1f3))
* **machine:** skip the clean restore at the state just captured ([49fad44](https://github.com/This-Is-NPC/backstage/commit/49fad44fdd43ae10b3a42a46a4b0eff9b40dd071))
* **machine:** time restore, boot and capture ([f234776](https://github.com/This-Is-NPC/backstage/commit/f2347767269d9726a386837c9c164a9b21d8b1f7))
* **machine:** wait for image-catalog before a clean restore ([831a707](https://github.com/This-Is-NPC/backstage/commit/831a707854428a8ab5a48e2c43a048dba3543c98))
* **machine:** wait for the catalog during capture ([f84d77e](https://github.com/This-Is-NPC/backstage/commit/f84d77ed686f68cbfb95e28168f1806a9c735a46))
* **presentation:** cache encoded chunks by timeline event ([346d12c](https://github.com/This-Is-NPC/backstage/commit/346d12ca7a6c8635da173f8a4ce7265b0955d400))
* **presentation:** cache prepared tracks and mixed audio ([9a0a0b6](https://github.com/This-Is-NPC/backstage/commit/9a0a0b61b60c2e1d4e2c682043c2b299fc447d55))
* **presentation:** composite steady chunks with FFmpeg ([a898c9a](https://github.com/This-Is-NPC/backstage/commit/a898c9a7802b44a2e840e803af1cd773b4d01518))
* **presentation:** lease scene-source takes ([2cc7b1d](https://github.com/This-Is-NPC/backstage/commit/2cc7b1d8efb03b84ffa55c6696fae09376030fa6))
* **presentation:** prepare tracks in parallel and configure thread counts ([41690b0](https://github.com/This-Is-NPC/backstage/commit/41690b0d52e3a922cd920064ddedb1a634b18e0d))
* **presentation:** preview an interval at a draft scale ([1ffec53](https://github.com/This-Is-NPC/backstage/commit/1ffec53c0171b02b3a57ca049cb518901fe9f235))
* **presentation:** probe slot geometry and capture page layers ([d0ce853](https://github.com/This-Is-NPC/backstage/commit/d0ce853db88d4535aea4db6def06c12707ed60d5))
* **presentation:** record render phase timings ([8dba582](https://github.com/This-Is-NPC/backstage/commit/8dba582814bb23cecce14645c47c88b235c74f75))
* **presentation:** render timeline chunks in parallel ([ea51389](https://github.com/This-Is-NPC/backstage/commit/ea51389d3f166efad5abd1927a274f0eeb364aae))
* **presentation:** reuse layer stills for declared static layouts ([54e54f8](https://github.com/This-Is-NPC/backstage/commit/54e54f854cee0af63f91cabaabd373c46481c2b8))
* **presentation:** serve workspace assets ([ba39653](https://github.com/This-Is-NPC/backstage/commit/ba39653dd16b8bd5d00d6bdb84a27ab7cfab596f))
* **production:** import capture-failed and order vm-end ([6c26146](https://github.com/This-Is-NPC/backstage/commit/6c261466827dd38ce07368a75e3e84e0344c65ef))
* **production:** publish eligible scene takes ([197373d](https://github.com/This-Is-NPC/backstage/commit/197373dab035155aae73f3227f340474ec151d57))
* **scene:** add vm-end and end-state facts ([6deabaf](https://github.com/This-Is-NPC/backstage/commit/6deabaf6b70099bce4fbbe8777ecd4fb33d97d83))
* **scene:** declare inherited state groups ([29514f9](https://github.com/This-Is-NPC/backstage/commit/29514f99677d184b65012178b071d1fc68d21d36))
* **scene:** expose the extends root without full validation ([4a4e28b](https://github.com/This-Is-NPC/backstage/commit/4a4e28bb1e0154b516435eee003ff55f6f47430f))
* **scene:** hash take inputs ([8e52ac0](https://github.com/This-Is-NPC/backstage/commit/8e52ac0f4a10a7cf3d299a3d82432e51ab2be1dc))
* **scene:** inherit config through extends ([e2df47a](https://github.com/This-Is-NPC/backstage/commit/e2df47a3181992ccc820fba065b89d9c62b574e5))
* **take:** import a clip from another filesystem ([0adc7c5](https://github.com/This-Is-NPC/backstage/commit/0adc7c5885d279d2bf2a9a955cd6f927f4513ee8))
* **take:** return a typed error when no take is published ([8f5627e](https://github.com/This-Is-NPC/backstage/commit/8f5627efa07f928c8c01b98811f755a6901c5a6c))
* **workspace:** classify produced states for prune ([886a638](https://github.com/This-Is-NPC/backstage/commit/886a638842bbd8fdbe7ccc42dfcd16ef6cb6adc7))
* **workspace:** expose Evaluate and the --with-deps plan ([44c90d1](https://github.com/This-Is-NPC/backstage/commit/44c90d177e2e817514c42677767bbe853f068cd4))
* **workspace:** plan and report state groups as one unit ([f1c35f2](https://github.com/This-Is-NPC/backstage/commit/f1c35f23a9ba6304a54faac9371955fd4eb15437))
* **workspace:** plan every stale scene ([52bbc4b](https://github.com/This-Is-NPC/backstage/commit/52bbc4ba10f6f24d8a2f22d1c134faab269e970a))
* **workspace:** report take freshness across an extends root ([670a7cf](https://github.com/This-Is-NPC/backstage/commit/670a7cfcd94c8bfca030c4839f3db32081839e2a))


### Bug Fixes

* **cli:** accept the stale directory as an argument ([3173005](https://github.com/This-Is-NPC/backstage/commit/31730056bb9efa84f6ac74302693987630bbb19a))
* **machine:** keep a named read ACL on captured disks ([c3f5903](https://github.com/This-Is-NPC/backstage/commit/c3f59032fccd19a9e0fc5bc3bfa7ab0fd0b2efc1))
* **machine:** record a silent member's skipped restore ([428ced4](https://github.com/This-Is-NPC/backstage/commit/428ced48ea74d4da0bff65016b13c799a6535320))
* **presentation:** serialize progress writes from prepare workers ([19b20b3](https://github.com/This-Is-NPC/backstage/commit/19b20b3ed8033b8cf5f2fa164ad868785dbf8f41))
* **workspace:** remake every member of an incomplete group ([69f1d77](https://github.com/This-Is-NPC/backstage/commit/69f1d77d08b83ac47ff9443043afc2f6113761dc))


### Performance Improvements

* **machine:** allow eight delta layers by default ([ecab47f](https://github.com/This-Is-NPC/backstage/commit/ecab47fa7ed9747abcca193a15dabf89520a5cd7))

## [0.6.0](https://github.com/This-Is-NPC/backstage/compare/v0.5.0...v0.6.0) (2026-09-09)


### Features

* **presentation:** compose declarative videos ([aab13ef](https://github.com/This-Is-NPC/backstage/commit/aab13efe8695c037af3f1df298d630b1f3fcb25c))
* **presentation:** compose declarative videos ([2eedf41](https://github.com/This-Is-NPC/backstage/commit/2eedf41a67ba9710013ac579f1af43e7b68f8843))

## [0.5.0](https://github.com/This-Is-NPC/backstage/compare/v0.4.0...v0.5.0) (2026-09-09)


### Features

* **machine:** add managed Omarchy VM stages ([fbfa27c](https://github.com/This-Is-NPC/backstage/commit/fbfa27c129d390001da6ac16a6c8c1e89ccd9a9d))
* **machine:** add managed Omarchy VM stages ([f5a8ed2](https://github.com/This-Is-NPC/backstage/commit/f5a8ed2682853119bcc430af12bf5ae733fd7922))

## [0.4.0](https://github.com/This-Is-NPC/backstage/compare/v0.3.0...v0.4.0) (2026-09-08)


### Features

* **engine:** report a take shorter than its window ([15f7676](https://github.com/This-Is-NPC/backstage/commit/15f7676bbc059c256183bccae7842e0f3bd1a9b4))
* **engine:** report a take shorter than its window ([8bcd701](https://github.com/This-Is-NPC/backstage/commit/8bcd701e4033629a9ac8bf8a9c13380b365c0531))


### Bug Fixes

* **engine:** cap the slack a long take is given ([5959005](https://github.com/This-Is-NPC/backstage/commit/5959005e07a1538d2bacb28c1667720499f6c3bb))
* **recorder:** notice a wf-recorder still closing ([867a11e](https://github.com/This-Is-NPC/backstage/commit/867a11eccb467533a76edad0916e753174117bc1))

## [0.3.0](https://github.com/This-Is-NPC/backstage/compare/v0.2.1...v0.3.0) (2026-09-08)


### Features

* **guest:** drive an Omarchy guest as a stage ([976a7d2](https://github.com/This-Is-NPC/backstage/commit/976a7d29de1014ccf203e205bfbd9447876f2119))
* **produce:** play a stretch of a take at its own rate ([8f6d339](https://github.com/This-Is-NPC/backstage/commit/8f6d339c43cca07819513e6bf75fdc509da2f8e6))
* **recorder:** film a guest from outside it ([3ba7558](https://github.com/This-Is-NPC/backstage/commit/3ba7558713349ba2dd65af3238f111b6ebc79cbc))
* **scene:** let a scene choose which recorder films it ([8664486](https://github.com/This-Is-NPC/backstage/commit/8664486e953490e60d526e96087d3ec729864b9f))
* **scene:** let a scene name the vm it runs on ([77e382a](https://github.com/This-Is-NPC/backstage/commit/77e382a7c607db796bfce1dfdd4da36d92dbd995))
* **skill:** install the agent skill into ~/.agents ([8993bfa](https://github.com/This-Is-NPC/backstage/commit/8993bfae0832cb6627651a45aedc74c9153de99b))
* stage, drive and record a scene inside an Omarchy guest ([263a1c5](https://github.com/This-Is-NPC/backstage/commit/263a1c5db49bb5644b9aedc48f0a1311e8880879))
* **vm:** make the stage usable for a real take ([6861076](https://github.com/This-Is-NPC/backstage/commit/6861076c8eeef30596bc1af33bace236cfc8f5c5))
* **vm:** stage, record and type inside an Omarchy guest ([8d283d9](https://github.com/This-Is-NPC/backstage/commit/8d283d9dce7a7f69423e015a2ee7e66fb45dbeaf))

## [0.2.1](https://github.com/This-Is-NPC/backstage/compare/v0.2.0...v0.2.1) (2026-06-15)


### Bug Fixes

* **release:** cut Linux-only release ([29f85ea](https://github.com/This-Is-NPC/backstage/commit/29f85eac9c75da28ecafa9186013c9ab6e7c537b))
* **release:** cut Linux-only release ([3a91ade](https://github.com/This-Is-NPC/backstage/commit/3a91ade85ac8bb6b86006bdf00291692465f0337))

## [0.2.0](https://github.com/This-Is-NPC/backstage/compare/v0.1.0...v0.2.0) (2026-06-15)


### Features

* live transitions in prompter style with interrupt-safe recording ([608e334](https://github.com/This-Is-NPC/backstage/commit/608e334c5e0e12b34c144ceecfe7fd44458b9310))
* live transitions in prompter style with interrupt-safe recording ([7833589](https://github.com/This-Is-NPC/backstage/commit/7833589bb302a10afd4aad0f9e1dbe4ea81c9f85))

## 0.1.0 (2026-06-07)


### Features

* **config:** define local project productions ([820ec9a](https://github.com/This-Is-NPC/backstage/commit/820ec9a474bb69c450abb56ff9b6e6d495c24d7b))
* **installer:** add release asset installers ([bdde1fc](https://github.com/This-Is-NPC/backstage/commit/bdde1fc5026102a173996fac4be5420590a6100d))
* **installer:** redo v0.1.0 release setup ([5023454](https://github.com/This-Is-NPC/backstage/commit/50234549d23c1013d953a849ccc5ef158874572a))
* **production:** stitch multi-scene videos ([1ae00ae](https://github.com/This-Is-NPC/backstage/commit/1ae00ae84050f9e11e04d1f0af3d848467d7dbc1))
* **prompter:** narrate recordings on screen ([dd182fb](https://github.com/This-Is-NPC/backstage/commit/dd182fb72300ade6de830b5f8408c11a9bed8c14))
* **prop:** run external automation scripts ([7bff90c](https://github.com/This-Is-NPC/backstage/commit/7bff90c98b97c51ba51c3bf4168b9068b7fe46c9))
* **recorder:** capture workflows as mp4 tutorials ([2d581c3](https://github.com/This-Is-NPC/backstage/commit/2d581c3323de6e9e5cac15bd83bec4f92cbeacc3))
* **rehearsal:** validate scenes without recording ([95b5fdd](https://github.com/This-Is-NPC/backstage/commit/95b5fdd38b14120b881faa29aefaa15e21ec36fb))
* **scene:** script reusable software workflows ([79f80a8](https://github.com/This-Is-NPC/backstage/commit/79f80a809c5e972f5db1e5367bbf633341b56ac7))
