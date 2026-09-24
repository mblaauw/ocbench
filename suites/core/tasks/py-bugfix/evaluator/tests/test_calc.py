import unittest

from calc import add, divide


class AddTests(unittest.TestCase):
    def test_add(self):
        self.assertEqual(add(2, 3), 5)


class DivideTests(unittest.TestCase):
    def test_divides_evenly(self):
        self.assertEqual(divide(10, 2), 5.0)

    def test_divides_with_remainder(self):
        self.assertAlmostEqual(divide(7, 2), 3.5)

    def test_zero_division(self):
        with self.assertRaises(ZeroDivisionError):
            divide(1, 0)


if __name__ == "__main__":
    unittest.main()
