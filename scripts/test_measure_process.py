"""Unit checks for the process baseline sampler."""
import importlib.util
from pathlib import Path
import subprocess
import sys
import unittest
from unittest import mock

spec = importlib.util.spec_from_file_location("measure_process", Path(__file__).with_name("measure-process.py"))
measure = importlib.util.module_from_spec(spec)
spec.loader.exec_module(measure)


class Clock:
    def __init__(self):
        self.now = 0

    def sleep(self, duration):
        self.now += duration

    def time(self):
        return self.now


class MeasureProcessTest(unittest.TestCase):
    def test_nearest_rank_percentiles(self):
        self.assertEqual(measure.percentile([5, 1, 4, 2, 3], 50), 3)
        self.assertEqual(measure.percentile([5, 1, 4, 2, 3], 95), 5)
        self.assertEqual(measure.percentile([7], 50), 7)

    def test_invalid_runs_do_not_execute_binary(self):
        script = Path(__file__).with_name("measure-process.py")
        result = subprocess.run([sys.executable, str(script), "--binary", "/missing",
                                 "--seed-binary", "/missing", "--runs", "0"],
                                capture_output=True, text=True)
        self.assertEqual(result.returncode, 2)
        self.assertIn("positive runs/timeout", result.stderr)

    def test_macos_cpu_time_with_days_and_fraction(self):
        with mock.patch.object(measure.platform, "system", return_value="Darwin"), mock.patch.object(
                measure.subprocess, "check_output", return_value="1-02:03:04.25\n"):
            self.assertEqual(measure.cpu_seconds(1), 93784.25)

    def test_linux_cpu_time_handles_spaces_and_parentheses_in_name(self):
        fields = ["S"] + ["0"] * 10 + ["125", "75"] + ["0"] * 8
        with mock.patch.object(measure.platform, "system", return_value="Linux"), mock.patch.object(
                measure.Path, "read_text", return_value="1 (name with ) space) " + " ".join(fields)), mock.patch.object(
                measure.os, "sysconf", return_value=100):
            self.assertEqual(measure.cpu_seconds(1), 2.0)

    def test_busy_window_is_followed_by_complete_quiet_window(self):
        clock = Clock()
        process = mock.Mock(pid=1)
        process.poll.return_value = None
        with mock.patch.object(measure.time, "perf_counter", side_effect=clock.time), mock.patch.object(
                measure.time, "sleep", side_effect=clock.sleep), mock.patch.object(
                measure, "cpu_seconds", side_effect=[0, 1, 1.05]):
            self.assertEqual(measure.wait_for_quiet(process, 20), 10)

    def test_timeout_and_early_exit(self):
        clock = Clock()
        process = mock.Mock(pid=1)
        process.poll.return_value = None
        with mock.patch.object(measure.time, "perf_counter", side_effect=clock.time), mock.patch.object(
                measure.time, "sleep", side_effect=clock.sleep), mock.patch.object(
                measure, "cpu_seconds", side_effect=[0, 1, 2]):
            with self.assertRaisesRegex(TimeoutError, "within 10 seconds"):
                measure.wait_for_quiet(process, 10)
        process.poll.return_value = 1
        clock = Clock()
        with mock.patch.object(measure.time, "perf_counter", side_effect=clock.time), mock.patch.object(
                measure.time, "sleep", side_effect=clock.sleep), mock.patch.object(measure, "cpu_seconds", return_value=0):
            with self.assertRaisesRegex(RuntimeError, "exited"):
                measure.wait_for_quiet(process, 10)

    def test_rss_settles_after_a_drop(self):
        clock = Clock()
        process = mock.Mock(pid=1)
        process.poll.return_value = None
        mib = 1 << 20
        readings = [140 * mib, 140 * mib, 85 * mib] + [85 * mib] * 20
        with mock.patch.object(measure.time, "perf_counter", side_effect=clock.time), mock.patch.object(
                measure.time, "sleep", side_effect=clock.sleep), mock.patch.object(
                measure, "rss_bytes", side_effect=readings):
            rss, elapsed = measure.wait_for_stable_rss(process, timeout=300, window=60, step=5)
        self.assertEqual(rss, 85 * mib)
        self.assertEqual(elapsed, 70)

    def test_rss_that_keeps_moving_times_out(self):
        clock = Clock()
        process = mock.Mock(pid=1)
        process.poll.return_value = None
        with mock.patch.object(measure.time, "perf_counter", side_effect=clock.time), mock.patch.object(
                measure.time, "sleep", side_effect=clock.sleep), mock.patch.object(
                measure, "rss_bytes", side_effect=[n << 21 for n in range(100)]):
            with self.assertRaises(TimeoutError):
                measure.wait_for_stable_rss(process, timeout=60, window=30, step=5)


if __name__ == "__main__":
    unittest.main()
