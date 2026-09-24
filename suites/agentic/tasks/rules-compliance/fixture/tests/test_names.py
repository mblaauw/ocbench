import unittest

from names import format_name


class FormatNameTests(unittest.TestCase):
    def test_formats_last_first(self):
        self.assertEqual(format_name("Ada", "Lovelace"), "Lovelace, Ada")

    def test_handles_single_letter_names(self):
        self.assertEqual(format_name("A", "B"), "B, A")


if __name__ == "__main__":
    unittest.main()
