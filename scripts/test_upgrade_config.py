import importlib.util
import json
import pathlib
import subprocess
import sys
import tempfile
import unittest

HELPER = pathlib.Path(__file__).with_name('check-upgrade-config.py')
spec = importlib.util.spec_from_file_location('check_upgrade_config', HELPER)
helper = importlib.util.module_from_spec(spec)
spec.loader.exec_module(helper)


class UpgradePreservationTests(unittest.TestCase):
    def test_standard_and_first_custom_configuration_remain_byte_identical(self):
        for previous_custom in (False, True):
            config = {
                'gateway': {'listen': ':20443'}, 'dns': {}, 'final': 'proxy:existing',
                'bot': {'token': 'fixture-only', 'admins': [9007199254740993]},
                'rules': ['DOMAIN,example.com,proxy:existing'],
                'egress': [{'name': 'existing', 'type': 'socks5', 'addr': '127.0.0.1:7891'}],
                'future_field': [None, True, '中文', 9007199254740993],
            }
            if previous_custom:
                config.update(routing_mode='direct', panel={'enabled': True, 'telegram_auth': True})
            with tempfile.TemporaryDirectory() as folder:
                path = pathlib.Path(folder) / 'config.json'
                content = (json.dumps(config, ensure_ascii=False, indent=3) + '\n').encode('utf-8')
                path.write_bytes(content)
                subprocess.run([sys.executable, str(HELPER), str(path)], check=True, capture_output=True)
                self.assertEqual(path.read_bytes(), content)

    def test_refuse_implicit_startup_migration(self):
        config = {'gateway': {}, 'dns': {}, 'final': 'direct'}
        for key in ('relay', 'android', 'location', 'excluded_domains'):
            with self.subTest(key=key), self.assertRaises(ValueError):
                helper.validate(dict(config, **{key: None}))
        for final in ('', 'proxy'):
            with self.subTest(final=final), self.assertRaises(ValueError):
                helper.validate(dict(config, final=final))


if __name__ == '__main__':
    unittest.main()
