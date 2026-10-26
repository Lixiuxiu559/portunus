import base64
import hashlib
import io
from contextlib import redirect_stdout
from pathlib import Path
import tempfile
import unittest
from unittest.mock import Mock, patch

import yaml

from sync_cos import BUCKET, MANIFESTS, check_latest, prepare_files, sync_files, upload_progress, create_cos_client, credentials, release_info, download_release, formal_versions, prune_old_versions, main


class SyncTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.asset = self.root / "Portunus-0.5.0-mac-arm64.zip"
        self.asset.write_bytes(b"installer test payload")
        self.digest = base64.b64encode(hashlib.sha512(self.asset.read_bytes()).digest()).decode()
        for name in MANIFESTS:
            self.write_manifest(name)
        (self.root / (self.asset.name + ".blockmap")).write_text("blockmap")

    def write_manifest(self, name, **overrides):
        data = {
            "version": "0.5.0", "path": self.asset.name,
            "files": [{"url": self.asset.name, "sha512": self.digest, "size": self.asset.stat().st_size}],
        }
        data.update(overrides)
        (self.root / name).write_text(yaml.safe_dump(data))

    def test_installers_and_blockmaps_precede_all_manifests(self):
        assets, manifests = prepare_files(self.root, "v0.5.0")
        client = Mock()
        guard = Mock(side_effect=lambda: self.assertEqual(client.upload_file.call_count, len(assets)))
        sync_files(client, assets, manifests, guard)
        guard.assert_called_once()
        calls = client.upload_file.call_args_list
        self.assertEqual([call.kwargs["Key"] for call in calls], ["releases/" + p.name for p in assets + manifests])
        for call in calls:
            self.assertEqual(call.kwargs["Bucket"], BUCKET)
        for call in calls[len(assets):]:
            self.assertEqual(call.kwargs["CacheControl"], "no-cache")

    def test_upload_failure_preserves_existing_manifests(self):
        assets, manifests = prepare_files(self.root, "v0.5.0")
        client = Mock()
        client.upload_file.side_effect = RuntimeError("upload failed")
        guard = Mock()
        with self.assertRaisesRegex(RuntimeError, "upload failed"):
            sync_files(client, assets, manifests, guard)
        guard.assert_not_called()
        self.assertFalse(any(call.kwargs["Key"].endswith(".yml") for call in client.upload_file.call_args_list))

    def test_newer_release_during_upload_prevents_manifest_publish(self):
        assets, manifests = prepare_files(self.root, "v0.5.0")
        client = Mock()
        with self.assertRaisesRegex(ValueError, "newer release"):
            sync_files(client, assets, manifests, Mock(side_effect=ValueError("newer release")))
        self.assertEqual(client.upload_file.call_count, len(assets))

    def test_incomplete_platform_manifests_fail_before_upload(self):
        (self.root / MANIFESTS[-1]).unlink()
        with self.assertRaises(FileNotFoundError):
            prepare_files(self.root, "v0.5.0")

    def test_wrong_version_is_rejected(self):
        self.write_manifest(MANIFESTS[0], version="0.4.0")
        with self.assertRaisesRegex(ValueError, "版本"):
            prepare_files(self.root, "v0.5.0")

    def test_new_mac_release_requires_signature_before_upload(self):
        new_asset = self.root / "Portunus-0.5.1-mac-arm64.zip"
        new_asset.write_bytes(self.asset.read_bytes())
        for name in MANIFESTS:
            self.write_manifest(name, version="0.5.1", path=new_asset.name,
                                files=[{"url": new_asset.name, "sha512": self.digest, "size": new_asset.stat().st_size}])
        with self.assertRaisesRegex(ValueError, "缺少更新签名"):
            prepare_files(self.root, "v0.5.1")
        import json
        signature = new_asset.with_name(new_asset.name + ".sig")
        signature.write_text(json.dumps({"version": "0.5.1", "arch": "arm64", "size": new_asset.stat().st_size,
                                        "sha512": self.digest, "signature": base64.b64encode(bytes(64)).decode()}))
        assets, _ = prepare_files(self.root, "v0.5.1")
        self.assertIn(signature, assets)
        data = json.loads(signature.read_text())
        data["version"] = "0.5.0"
        signature.write_text(json.dumps(data))
        with self.assertRaisesRegex(ValueError, "元数据不匹配"):
            prepare_files(self.root, "v0.5.1")

    def test_corrupted_package_is_rejected(self):
        self.asset.write_bytes(b"corrupted")
        with self.assertRaisesRegex(ValueError, "SHA-512"):
            prepare_files(self.root, "v0.5.0")

    def test_missing_package_is_rejected(self):
        self.asset.unlink()
        with self.assertRaisesRegex(ValueError, "不存在"):
            prepare_files(self.root, "v0.5.0")

    def test_remote_and_traversing_paths_are_rejected(self):
        for url in ["https://other.example/app.zip", "../app.zip", "%2e%2e%2fapp.zip", "app.zip?token=123"]:
            with self.subTest(url=url):
                self.write_manifest(MANIFESTS[0], files=[{"url": url, "sha512": self.digest}])
                with self.assertRaisesRegex(ValueError, "非同目录"):
                    prepare_files(self.root, "v0.5.0")

    @patch.dict("os.environ", {"GITHUB_REPOSITORY": "owner/repo"})
    @patch("sync_cos.subprocess.check_output")
    def test_only_latest_release_can_be_synced(self, output):
        output.return_value = "v0.5.0\n"
        check_latest("v0.5.0")
        with self.assertRaisesRegex(ValueError, "不是 GitHub 最新正式版"):
            check_latest("v0.4.0")


    def test_upload_progress_is_reported_at_ten_percent_intervals(self):
        output = io.StringIO()
        report = upload_progress("installer.zip")
        with redirect_stdout(output):
            for consumed in [1, 5, 10, 11, 20, 99, 100]:
                report(consumed, 100)
        self.assertEqual(output.getvalue().splitlines(), [
            "上传进度：installer.zip 0%", "上传进度：installer.zip 10%",
            "上传进度：installer.zip 20%", "上传进度：installer.zip 90%",
            "上传进度：installer.zip 100%",
        ])


    @patch.dict("os.environ", {"COS_SECRET_ID": "test-id", "COS_SECRET_KEY": "test-key"})
    def test_upload_uses_official_public_domain(self):
        client = create_cos_client()
        self.assertEqual(
            client._conf.uri(bucket=BUCKET, path="releases/test"),
            "https://portunus-1321735248.cos.ap-shanghai.tencentcos.cn/releases/test",
        )


    @patch.dict("os.environ", {}, clear=True)
    @patch("sync_cos.sys.stdin.isatty", return_value=False)
    def test_missing_local_credentials_gives_actionable_error(self, _):
        with self.assertRaisesRegex(ValueError, "本地终端"):
            credentials()

    @patch.dict("os.environ", {"TENCENTCLOUD_SECRET_ID": "local-id", "TENCENTCLOUD_SECRET_KEY": "local-key"}, clear=True)
    def test_tencent_cloud_environment_credentials_are_supported(self):
        self.assertEqual(credentials(), ("local-id", "local-key"))

    @patch("sync_cos.subprocess.check_output")
    def test_prerelease_is_rejected_before_upload(self, output):
        output.return_value = '{"tagName":"v0.6.0-beta.1","isDraft":false,"isPrerelease":true}'
        with self.assertRaisesRegex(ValueError, "正式 Release"):
            release_info("v0.6.0-beta.1")

    @patch("sync_cos.subprocess.check_output")
    def test_local_latest_version_is_resolved_and_verified(self, output):
        output.side_effect = [
            '{"tagName":"v0.5.0","isDraft":false,"isPrerelease":false}',
            'v0.5.0\n',
        ]
        self.assertEqual(release_info(None), "v0.5.0")

    @patch("sync_cos.subprocess.run")
    def test_local_download_refreshes_manifests_and_reuses_packages(self, run):
        download_release("v0.5.0", self.root)
        self.assertEqual(run.call_count, 2)
        self.assertIn("--clobber", run.call_args_list[0].args[0])
        self.assertIn("--skip-existing", run.call_args_list[1].args[0])
        self.assertTrue(all(call.kwargs["check"] for call in run.call_args_list))


    @patch("sync_cos.prune_old_versions")
    @patch("sync_cos.check_latest")
    @patch("sync_cos.create_cos_client")
    @patch("sync_cos.release_info", return_value="v0.5.0")
    def test_cleanup_runs_only_after_every_upload_succeeds(self, release, create_client, check, prune):
        assets, manifests = prepare_files(self.root, "v0.5.0")
        for failed_index in [0, len(assets), len(assets) + len(manifests) - 1, None]:
            with self.subTest(failed_index=failed_index):
                prune.reset_mock()
                client = Mock()
                create_client.return_value = client
                if failed_index is not None:
                    client.upload_file.side_effect = [None] * failed_index + [RuntimeError("upload failed")]
                    with self.assertRaisesRegex(RuntimeError, "upload failed"):
                        main(["--tag", "v0.5.0", "--assets-dir", str(self.root)])
                    prune.assert_not_called()
                else:
                    prune.side_effect = lambda *_: self.assertEqual(client.upload_file.call_count, len(assets) + len(manifests))
                    main(["--tag", "v0.5.0", "--assets-dir", str(self.root)])
                    prune.assert_called_once_with(client, "v0.5.0")


class RetentionTests(unittest.TestCase):
    def setUp(self):
        self.versions = ["0.4.0", "0.3.0", "0.2.0", "0.1.0"]
        self.keys = [self.key(version) for version in self.versions]
        self.client = Mock()
        self.client.list_objects.return_value = self.page(self.keys)
        self.formal_patch = patch("sync_cos.formal_versions", side_effect=lambda: self.versions)
        self.formal = self.formal_patch.start()
        self.addCleanup(self.formal_patch.stop)
        guard_patch = patch("sync_cos.check_latest")
        self.guard = guard_patch.start()
        self.addCleanup(guard_patch.stop)

    @staticmethod
    def key(version, platform="mac-arm64.zip"):
        return f"releases/Portunus-{version}-{platform}"

    @staticmethod
    def page(keys, **extra):
        return {"Contents": [{"Key": key} for key in keys], "IsTruncated": "false", **extra}

    def deleted(self):
        return [call.kwargs["Key"] for call in self.client.delete_object.call_args_list]

    def test_keep_three_versions_and_delete_all_old_platform_assets(self):
        old = [self.key("0.1.0", platform) for platform in [
            "mac-arm64.zip", "mac-arm64.zip.blockmap", "mac-arm64.dmg",
            "windows-x64.exe", "windows-x64.exe.blockmap", "windows-x64.zip",
            "linux-x86_64.AppImage",
        ]]
        self.client.list_objects.return_value = self.page(self.keys[:3] + old)
        prune_old_versions(self.client, "v0.4.0")
        self.assertEqual(self.deleted(), sorted(old))
        self.guard.assert_called_once_with("v0.4.0")
        for call in self.client.delete_object.call_args_list:
            self.assertEqual(call.kwargs["Bucket"], BUCKET)

    def test_fixed_names_manifests_unknown_and_prerelease_files_are_preserved(self):
        protected = [
            "releases/latest.yml", "releases/latest-mac.yml", "releases/latest-linux.yml",
            "releases/Portunus-mac-arm64.zip", "releases/Portunus-linux-x64.AppImage",
            "releases/Portunus-0.1.0-notes.zip", "releases/notes.txt",
            self.key("0.0.9"), self.key("0.1.0-beta.1"),
            "releases/sub/Portunus-0.1.0-mac-arm64.zip", "other/Portunus-0.1.0-mac-arm64.zip",
        ]
        self.client.list_objects.return_value = self.page(self.keys + protected)
        prune_old_versions(self.client, "v0.4.0")
        self.assertEqual(self.deleted(), [self.key("0.1.0")])

    def test_unsynced_github_versions_do_not_consume_retention_slots(self):
        self.versions = ["0.4.0", "0.3.0", "0.2.0", "0.1.0", "0.0.9"]
        self.client.list_objects.return_value = self.page([self.key(v) for v in ["0.4.0", "0.1.0", "0.0.9"]])
        prune_old_versions(self.client, "v0.4.0")
        self.assertEqual(self.deleted(), [])

    def test_current_version_is_preserved_even_if_publish_dates_differ(self):
        self.versions = ["0.3.0", "0.2.0", "0.1.0", "0.4.0"]
        prune_old_versions(self.client, "v0.4.0")
        self.assertEqual(self.deleted(), [self.key("0.1.0")])

    def test_cos_pagination_is_fully_read_before_deletion(self):
        self.client.list_objects.side_effect = [
            self.page(self.keys[:2], IsTruncated="true", NextMarker=self.keys[1]),
            self.page(self.keys[2:]),
        ]
        self.client.delete_object.side_effect = lambda **_: self.assertEqual(self.client.list_objects.call_count, 2)
        prune_old_versions(self.client, "v0.4.0")
        self.assertEqual(self.deleted(), [self.key("0.1.0")])
        self.assertEqual(self.client.list_objects.call_args_list[1].kwargs["Marker"], self.keys[1])

    def test_failed_listing_or_invalid_pagination_does_not_delete(self):
        for response in [RuntimeError("list denied"), self.page(self.keys, IsTruncated="true"), self.page(self.keys, IsTruncated="invalid")]:
            with self.subTest(response=response):
                self.client.reset_mock(side_effect=True)
                self.client.list_objects.side_effect = [response]
                with self.assertRaises((RuntimeError, ValueError)):
                    prune_old_versions(self.client, "v0.4.0")
                self.assertEqual(self.deleted(), [])

    def test_release_lookup_failure_or_newer_release_does_not_delete(self):
        self.formal.side_effect = RuntimeError("GitHub offline")
        with self.assertRaisesRegex(RuntimeError, "GitHub offline"):
            prune_old_versions(self.client, "v0.4.0")
        self.assertEqual(self.deleted(), [])
        self.formal.side_effect = lambda: self.versions
        self.guard.side_effect = ValueError("newer release")
        with self.assertRaisesRegex(ValueError, "newer release"):
            prune_old_versions(self.client, "v0.4.0")
        self.assertEqual(self.deleted(), [])

    def test_missing_current_version_prevents_deletion(self):
        self.versions = ["0.3.0", "0.2.0", "0.1.0"]
        with self.assertRaisesRegex(ValueError, "缺少当前版本"):
            prune_old_versions(self.client, "v0.4.0")
        self.versions.insert(0, "0.4.0")
        self.client.list_objects.return_value = self.page(self.keys[1:])
        with self.assertRaisesRegex(ValueError, "未找到当前版本"):
            prune_old_versions(self.client, "v0.4.0")
        self.assertEqual(self.deleted(), [])

    def test_delete_permission_failure_is_propagated(self):
        self.client.delete_object.side_effect = RuntimeError("delete denied")
        with self.assertRaisesRegex(RuntimeError, "delete denied"):
            prune_old_versions(self.client, "v0.4.0")
        self.assertEqual(self.deleted(), [self.key("0.1.0")])

    def test_formal_release_records_are_filtered_sorted_and_deduplicated(self):
        self.formal_patch.stop()
        releases = [[
            {"tag_name": "v0.1.0", "draft": False, "prerelease": False, "published_at": "2026-01-01T00:00:00Z"},
            {"tag_name": "v0.3.0", "draft": False, "prerelease": True, "published_at": "2026-03-01T00:00:00Z"},
            {"tag_name": "v0.4.0", "draft": True, "prerelease": False, "published_at": "2026-04-01T00:00:00Z"},
        ], [
            {"tag_name": "v0.2.0", "draft": False, "prerelease": False, "published_at": "2026-02-01T00:00:00Z"},
            {"tag_name": "0.2.0", "draft": False, "prerelease": False, "published_at": "2026-01-31T00:00:00Z"},
        ]]
        import json
        with patch("sync_cos.subprocess.check_output", return_value=json.dumps(releases)) as output:
            self.assertEqual(formal_versions(), ["0.2.0", "0.1.0"])
            self.assertIn("--paginate", output.call_args.args[0])
