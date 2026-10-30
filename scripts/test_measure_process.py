"""Unit checks for the process baseline sampler."""
import importlib.util
from pathlib import Path
import subprocess
import sys
import unittest

SCRIPT = Path(__file__).with_name("measure-process.py")
spec = importlib.util.spec_from_file_location("measure_process", SCRIPT)
sampler = importlib.util.module_from_spec(spec)
spec.loader.exec_module(sampler)


class ProcessSamplerTest(unittest.TestCase):
    def test_nearest_rank_percentiles(self):
        self.assertEqual(sampler.percentile([5, 1, 4, 2, 3], 50), 3)
        self.assertEqual(sampler.percentile([5, 1, 4, 2, 3], 95), 5)
        self.assertEqual(sampler.percentile([7], 50), 7)

    def test_invalid_runs_do_not_execute_binary(self):
        result = subprocess.run([sys.executable, str(SCRIPT), "--binary", "/missing",
                                 "--seed-binary", "/missing", "--runs", "0"],
                                capture_output=True, text=True)
        self.assertEqual(result.returncode, 2)
        self.assertIn("positive runs/timeout", result.stderr)


if __name__ == "__main__":
    unittest.main()
