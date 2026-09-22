import unittest

from app.config import load_config


class ConfigTests(unittest.TestCase):
    def test_loads_without_error(self):
        load_config("config/app.yaml")

    def test_retries(self):
        self.assertEqual(load_config("config/app.yaml")["retries"], 3)

    def test_timeout_seconds(self):
        self.assertEqual(load_config("config/app.yaml")["timeout_seconds"], 30)

    def test_name(self):
        self.assertEqual(load_config("config/app.yaml")["name"], "demo")


if __name__ == "__main__":
    unittest.main()
