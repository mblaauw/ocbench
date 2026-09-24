import unittest

from calc import scale


class ScaleTests(unittest.TestCase):
    def test_scales_up(self):
        self.assertEqual(scale(3, 4), 12)

    def test_scales_by_zero(self):
        self.assertEqual(scale(5, 0), 0)

    def test_scales_floats(self):
        self.assertAlmostEqual(scale(2.5, 2), 5.0)


if __name__ == "__main__":
    unittest.main()
