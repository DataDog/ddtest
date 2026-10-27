package example;
public class ExampleTest {
    @org.testng.annotations.Test public void works() { org.testng.Assert.assertEquals(2 + 2, 4); }
    @org.testng.annotations.DataProvider(name = "rows") public Object[][] rows() {
        return new Object[][] {{"alpha"}, {"beta<&"}, {" alpha "}};
    }
    @org.testng.annotations.Test(dataProvider = "rows") public void row(String value) {
        org.testng.Assert.assertNotNull(value);
    }
}
