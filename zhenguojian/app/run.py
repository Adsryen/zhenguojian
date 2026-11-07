import sys

if "--selftest" in sys.argv:
    from zhenguojian.selftest import main as selftest_main

    sys.exit(selftest_main())

from zhenguojian.main import main

sys.exit(main())
