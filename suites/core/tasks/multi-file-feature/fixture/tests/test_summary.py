import json
import unittest

from src.report import render
from src.summary import summarize


class SummarizeTests(unittest.TestCase):
    def test_summary_of_values(self):
        self.assertEqual(
            summarize([1, 2, 3, 4]),
            {"count": 4, "sum": 10, "mean": 2.5, "min": 1, "max": 4},
        )

    def test_summary_of_single_value(self):
        self.assertEqual(
            summarize([5]),
            {"count": 1, "sum": 5, "mean": 5.0, "min": 5, "max": 5},
        )

    def test_empty_raises(self):
        with self.assertRaises(ValueError):
            summarize([])


class RenderTests(unittest.TestCase):
    def test_render_is_json(self):
        result = {"count": 1, "sum": 5, "mean": 5.0, "min": 5, "max": 5}
        self.assertEqual(json.loads(render(result)), result)


if __name__ == "__main__":
    unittest.main()
